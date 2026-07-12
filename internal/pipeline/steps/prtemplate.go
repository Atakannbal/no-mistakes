package steps

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
)

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
}

// loadPRTemplate reads and parses the repo's configured pr.template file, if
// any. Returns (nil, false) when no template is configured, and also (nil,
// false) - with a warning logged - when the configured template can't be
// read or parsed, so callers fall back to the built-in body layout instead
// of failing the run over a template typo.
func loadPRTemplate(sctx *pipeline.StepContext) (*template.Template, bool) {
	if sctx.Config == nil {
		return nil, false
	}
	path := strings.TrimSpace(sctx.Config.PR.Template)
	if path == "" {
		return nil, false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(sctx.WorkDir, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("pr.template unreadable, falling back to built-in PR body", "path", sctx.Config.PR.Template, "error", err)
		return nil, false
	}
	tmpl, err := template.New("pr-body").Parse(string(raw))
	if err != nil {
		slog.Warn("pr.template failed to parse, falling back to built-in PR body", "path", sctx.Config.PR.Template, "error", err)
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
// present and valid, otherwise the built-in section layout.
func finalizePRBody(sctx *pipeline.StepContext, title, branch, whatChanged, riskLine, testingMD, pipelineMD string, bodyLimit int) string {
	if tmpl, ok := loadPRTemplate(sctx); ok {
		rendered, err := renderPRBodyFromTemplate(tmpl, title, branch, whatChanged, cleanedUserIntent(sctx), riskLine, testingMD, pipelineMD, bodyLimit)
		if err != nil {
			slog.Warn("pr.template failed to render, falling back to built-in PR body", "path", sctx.Config.PR.Template, "error", err)
		} else {
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
func renderPRBodyFromTemplate(tmpl *template.Template, title, branch, whatChanged, intentText, riskLine, testingMD, pipelineMD string, bodyLimit int) (string, error) {
	data := prTemplateData{
		Title:       title,
		Branch:      branch,
		WhatChanged: stripWhatChangedHeading(whatChanged),
		Intent:      intentText,
		Risk:        riskLine,
		Testing:     testingMD,
		Pipeline:    pipelineMD,
	}
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
