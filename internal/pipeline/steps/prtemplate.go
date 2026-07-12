package steps

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/kunchenguid/no-mistakes/internal/conventional"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
)

// defaultJiraPattern matches a project-key-then-number ticket ID like
// "PROJ-123" wherever it appears in a branch name. It intentionally
// requires an uppercase key so it doesn't match arbitrary lowercase
// branch-name segments.
var defaultJiraPattern = regexp.MustCompile(`[A-Z][A-Z0-9]+-[0-9]+`)

// prTitleTemplateData is the set of placeholders available to a custom PR
// title template configured via pr.title_template in .no-mistakes.yaml.
type prTitleTemplateData struct {
	Type        string
	Scope       string
	Description string
	JiraTicket  string
	Branch      string
}

// extractJiraTicket pulls a ticket ID out of the branch name using either
// the repo's configured pr.jira_pattern or defaultJiraPattern. Returns ""
// when nothing matches.
func extractJiraTicket(sctx *pipeline.StepContext, branch string) string {
	re := defaultJiraPattern
	if sctx.Config != nil && sctx.Config.PR.JiraPattern != "" {
		compiled, err := regexp.Compile(sctx.Config.PR.JiraPattern)
		if err != nil {
			slog.Warn("pr.jira_pattern invalid, using default pattern", "pattern", sctx.Config.PR.JiraPattern, "error", err)
		} else {
			re = compiled
		}
	}
	return re.FindString(branch)
}

// renderPRTitleFromTemplate renders a configured pr.title_template against
// the components of the built-in, already-tightened conventional-commit
// title plus a branch-derived Jira ticket ID. Returns ("", false) when no
// template is configured, or when parsing/rendering fails - callers keep
// the built-in title in that case.
func renderPRTitleFromTemplate(sctx *pipeline.StepContext, builtinTitle, branch string) (string, bool) {
	if sctx.Config == nil {
		return "", false
	}
	tmplText := strings.TrimSpace(sctx.Config.PR.TitleTemplate)
	if tmplText == "" {
		return "", false
	}
	tmpl, err := template.New("pr-title").Parse(tmplText)
	if err != nil {
		slog.Warn("pr.title_template failed to parse, using built-in title", "error", err)
		return "", false
	}

	typ, scope, description, ok := conventional.ParseTitle(builtinTitle)
	if !ok {
		description = builtinTitle
	}
	data := prTitleTemplateData{
		Type:        typ,
		Scope:       scope,
		Description: description,
		JiraTicket:  extractJiraTicket(sctx, branch),
		Branch:      branch,
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		slog.Warn("pr.title_template failed to render, using built-in title", "error", err)
		return "", false
	}
	rendered := strings.TrimSpace(b.String())
	if rendered == "" {
		return "", false
	}
	return rendered, true
}

// prTemplateData is the set of placeholders available to a custom PR body
// template configured via pr.template in .no-mistakes.yaml.
type prTemplateData struct {
	Title       string
	Branch      string
	WhatChanged string
	Intent      string
	Risk        string
	Testing     string
	Pipeline    string
	JiraTicket  string
}

// autoDetectPRTemplatePaths are repo-standard GitHub PR template locations
// (relative to the repo root) checked, in order, when pr.template isn't
// configured. GitHub itself accepts either casing, so both are checked.
var autoDetectPRTemplatePaths = []string{
	filepath.Join(".github", "pull_request_template.md"),
	filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"),
}

// loadPRTemplateContent resolves and reads the repo's PR body template
// source: the explicitly configured pr.template file when set, otherwise
// the first repo-standard GitHub PR template found relative to sctx.WorkDir
// (.github/pull_request_template.md or .github/PULL_REQUEST_TEMPLATE.md).
// Returns ("", "", false) when neither is present, and also ("", "", false)
// - with a warning logged - when a configured template can't be read, so
// callers fall back to the built-in body layout instead of failing the run
// over a missing or unreadable file.
func loadPRTemplateContent(sctx *pipeline.StepContext) (raw, path string, ok bool) {
	configured := ""
	if sctx.Config != nil {
		configured = strings.TrimSpace(sctx.Config.PR.Template)
	}
	if configured != "" {
		path = configured
		if !filepath.IsAbs(path) {
			path = filepath.Join(sctx.WorkDir, path)
		}
	} else {
		for _, candidate := range autoDetectPRTemplatePaths {
			full := filepath.Join(sctx.WorkDir, candidate)
			if _, err := os.Stat(full); err == nil {
				path = full
				break
			}
		}
		if path == "" {
			return "", "", false
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("pr.template unreadable, falling back to built-in PR body", "path", path, "error", err)
		return "", "", false
	}
	return string(b), path, true
}

// loadPRTemplate reads and parses the repo's PR body template as a Go
// text/template. Returns (nil, false) when no template is found (see
// loadPRTemplateContent), and also (nil, false) - with a warning logged -
// when a configured template can't be parsed, so callers fall back to the
// built-in body layout instead of failing the run over a template typo.
func loadPRTemplate(sctx *pipeline.StepContext) (*template.Template, bool) {
	raw, path, ok := loadPRTemplateContent(sctx)
	if !ok {
		return nil, false
	}
	tmpl, err := template.New("pr-body").Parse(raw)
	if err != nil {
		slog.Warn("pr.template failed to parse, falling back to built-in PR body", "path", path, "error", err)
		return nil, false
	}
	return tmpl, true
}

// stripWhatChangedHeading removes a single leading "## What Changed" heading
// line from agent-produced body text, since a custom template supplies its
// own heading/formatting around the {{.WhatChanged}} placeholder.
func stripWhatChangedHeading(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	lines := strings.SplitN(trimmed, "\n", 2)
	heading := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "##"))
	heading = strings.TrimRight(heading, ":.!? ")
	if !strings.EqualFold(heading, "what changed") {
		return trimmed
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// finalizePRBody renders the final PR body: a configured pr.template when
// present and valid, otherwise the built-in section layout. A resolved
// template containing "{{" is parsed and rendered as a Go text/template,
// exactly as before; one with no "{{" anywhere is instead treated as an
// ordinary heading-only markdown template and filled via the
// fillHeadingOnlyPRTemplate heuristic, so plain "## Description"-style
// templates get their blank sections auto-filled too.
func finalizePRBody(sctx *pipeline.StepContext, title, branch, whatChanged, riskLine, testingMD, pipelineMD string, bodyLimit int) string {
	if raw, path, ok := loadPRTemplateContent(sctx); ok {
		data := prTemplateData{
			Title:       title,
			Branch:      branch,
			WhatChanged: stripWhatChangedHeading(whatChanged),
			Intent:      cleanedUserIntent(sctx),
			Risk:        riskLine,
			Testing:     testingMD,
			Pipeline:    pipelineMD,
			JiraTicket:  extractJiraTicket(sctx, branch),
		}
		if containsTemplateAction(raw) {
			tmpl, err := template.New("pr-body").Parse(raw)
			if err != nil {
				slog.Warn("pr.template failed to parse, falling back to built-in PR body", "path", path, "error", err)
			} else if rendered, err := renderPRBodyFromTemplateData(tmpl, data, bodyLimit); err != nil {
				slog.Warn("pr.template failed to render, falling back to built-in PR body", "path", path, "error", err)
			} else {
				return rendered
			}
		} else {
			rendered := strings.TrimSpace(fillHeadingOnlyPRTemplate(raw, data))
			if bodyLimit > 0 && scm.PRBodyLen(rendered) > bodyLimit {
				rendered = scm.ClampPRBody(rendered, bodyLimit)
			}
			return rendered
		}
	}
	if bodyLimit > 0 {
		return assemblePRBody(sctx, whatChanged, riskLine, testingMD, pipelineMD, bodyLimit)
	}
	return buildPRBody(whatChanged, riskLine, testingMD, pipelineMD, sctx)
}

// renderPRBodyFromTemplate executes a custom pr.template against the
// generated section content and, when bodyLimit is set, clamps the result
// the same way the built-in layout does as a last-resort backstop. Unlike
// the built-in layout it has no section-by-section budget trimming - a
// custom template is expected to be shaped by its author to fit.
func renderPRBodyFromTemplate(tmpl *template.Template, title, branch, whatChanged, intentText, jiraTicket, riskLine, testingMD, pipelineMD string, bodyLimit int) (string, error) {
	data := prTemplateData{
		Title:       title,
		Branch:      branch,
		WhatChanged: stripWhatChangedHeading(whatChanged),
		Intent:      intentText,
		Risk:        riskLine,
		Testing:     testingMD,
		Pipeline:    pipelineMD,
		JiraTicket:  jiraTicket,
	}
	return renderPRBodyFromTemplateData(tmpl, data, bodyLimit)
}

// renderPRBodyFromTemplateData is renderPRBodyFromTemplate's data-struct
// variant: it executes tmpl against an already-built prTemplateData instead
// of individual string arguments, which finalizePRBody uses so the same
// data feeds both the Go-template and heading-heuristic rendering paths.
func renderPRBodyFromTemplateData(tmpl *template.Template, data prTemplateData, bodyLimit int) (string, error) {
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", err
	}
	rendered := strings.TrimSpace(b.String())
	if bodyLimit > 0 && scm.PRBodyLen(rendered) > bodyLimit {
		rendered = scm.ClampPRBody(rendered, bodyLimit)
	}
	return rendered, nil
}

// containsTemplateAction reports whether raw contains any Go text/template
// action delimiter ("{{"). A resolved pr.template with no "{{" anywhere is
// treated as an ordinary heading-only markdown template (see
// fillHeadingOnlyPRTemplate) rather than parsed as a Go template, so an
// author who never opted into {{.Field}} syntax keeps their headings intact
// instead of silently seeing them reproduced verbatim forever.
func containsTemplateAction(raw string) bool {
	return strings.Contains(raw, "{{")
}

// prTemplateHeadingSynonyms maps a normalized (lowercased, punctuation-
// trimmed) markdown "## Heading" text to the prTemplateData field name it
// should be filled from when the section body is blank. Matching is
// intentionally conservative - only these known synonyms match - because a
// wrong match would silently overwrite an unrelated human-authored section.
var prTemplateHeadingSynonyms = map[string]string{
	"description": "Intent",
	"summary":     "Intent",
	"overview":    "Intent",

	"changes":      "WhatChanged",
	"what changed": "WhatChanged",
	"key changes":  "WhatChanged",

	"testing":   "Testing",
	"test plan": "Testing",
	"tests":     "Testing",

	"risk":            "Risk",
	"risk assessment": "Risk",
	"risks":           "Risk",

	"jira":        "JiraTicket",
	"ticket":      "JiraTicket",
	"jira ticket": "JiraTicket",
	"issue":       "JiraTicket",

	"pipeline": "Pipeline",
}

// normalizePRTemplateHeading strips the leading "##", surrounding
// whitespace, and trailing punctuation from a section heading line, then
// lowercases it for synonym matching.
func normalizePRTemplateHeading(line string) string {
	heading := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "##"))
	heading = strings.TrimRight(heading, ":.!? ")
	return strings.ToLower(strings.TrimSpace(heading))
}

// matchPRTemplateHeadingField returns the prTemplateData field name that a
// "## Heading" line's text should be filled from, or "" when the heading
// doesn't match any known category (e.g. "Affected Page(s)").
func matchPRTemplateHeadingField(headingLine string) string {
	return prTemplateHeadingSynonyms[normalizePRTemplateHeading(headingLine)]
}

// prTemplateFieldValue returns the data field named by field (as returned by
// matchPRTemplateHeadingField), or "" for an unrecognized name.
func prTemplateFieldValue(data prTemplateData, field string) string {
	switch field {
	case "Intent":
		return data.Intent
	case "WhatChanged":
		return data.WhatChanged
	case "Testing":
		return data.Testing
	case "Risk":
		return data.Risk
	case "JiraTicket":
		return data.JiraTicket
	case "Pipeline":
		return data.Pipeline
	default:
		return ""
	}
}

// htmlCommentPattern matches an HTML comment, including one spanning
// multiple lines, so guidance comments in a heading-only template (e.g.
// "<!-- Brief description of the change -->") can be preserved while
// checking whether the rest of a section is blank.
var htmlCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)

// sectionBodyIsBlank reports whether body - everything after a "## Heading"
// line up to the next heading or EOF - has no content once HTML comments
// are stripped. A body containing only guidance comments and whitespace is
// blank; anything else (including a bare placeholder like "- ") counts as
// existing content that must never be overwritten.
func sectionBodyIsBlank(body string) bool {
	stripped := htmlCommentPattern.ReplaceAllString(body, "")
	return strings.TrimSpace(stripped) == ""
}

// fillBlankSectionBody inserts value into a blank section body, placing it
// on its own line immediately after any existing guidance HTML comment (so
// the result reads like a human filled in the blank under the comment).
// When body has no comment, value simply becomes the section's content.
func fillBlankSectionBody(body, value string) string {
	loc := htmlCommentPattern.FindStringIndex(body)
	if loc == nil {
		return "\n" + value + "\n"
	}
	return strings.TrimRight(body[:loc[1]], "\n") + "\n" + value + "\n"
}

// fillHeadingOnlyPRTemplate fills a heading-only markdown PR template (one
// with no Go text/template "{{" actions) by matching each "## Heading" to a
// prTemplateData field via matchPRTemplateHeadingField and, only when both
// the heading matches AND the section body is blank AND the field has a
// non-empty value, inserting that value under the heading's guidance
// comment. Unmatched headings and non-blank sections are left byte-for-byte
// untouched, so existing human content is never overwritten.
func fillHeadingOnlyPRTemplate(raw string, data prTemplateData) string {
	sections := splitPRBodySections(raw)
	for i, section := range sections {
		lineEnd := strings.IndexByte(section, '\n')
		headingEnd := len(section)
		if lineEnd >= 0 {
			headingEnd = lineEnd + 1
		}
		headingLine := section[:headingEnd]
		if !isPRBodySectionHeading(headingLine) {
			continue
		}
		field := matchPRTemplateHeadingField(headingLine)
		if field == "" {
			continue
		}
		value := strings.TrimSpace(prTemplateFieldValue(data, field))
		if value == "" {
			continue
		}
		body := section[headingEnd:]
		if !sectionBodyIsBlank(body) {
			continue
		}
		sections[i] = headingLine + fillBlankSectionBody(body, value)
	}
	return joinPRBodySections(sections)
}
