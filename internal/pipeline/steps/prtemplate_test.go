package steps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
)

func TestLoadPRTemplate_AutoDetectsLowercaseGitHubTemplate(t *testing.T) {
	dir := t.TempDir()
	githubDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(githubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(githubDir, "pull_request_template.md"), []byte("## Summary\n\nfill me in\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sctx := &pipeline.StepContext{
		WorkDir: dir,
		Config:  &config.Config{},
	}

	tmpl, ok := loadPRTemplate(sctx)
	if !ok {
		t.Fatal("expected auto-detected template to be found")
	}
	rendered, err := renderPRBodyFromTemplate(tmpl, "", "", "", "", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "## Summary\n\nfill me in" {
		t.Fatalf("expected rendered auto-detected template content, got:\n%s", rendered)
	}
}

func TestLoadPRTemplate_AutoDetectsUppercaseGitHubTemplate(t *testing.T) {
	dir := t.TempDir()
	githubDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(githubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(githubDir, "PULL_REQUEST_TEMPLATE.md"), []byte("## Checklist\n\n- [ ] tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sctx := &pipeline.StepContext{
		WorkDir: dir,
		Config:  &config.Config{},
	}

	tmpl, ok := loadPRTemplate(sctx)
	if !ok {
		t.Fatal("expected auto-detected uppercase template to be found")
	}
	rendered, err := renderPRBodyFromTemplate(tmpl, "", "", "", "", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "## Checklist\n\n- [ ] tests" {
		t.Fatalf("expected rendered auto-detected template content, got:\n%s", rendered)
	}
}

func TestLoadPRTemplate_NoAutoDetectWhenNoGitHubTemplateExists(t *testing.T) {
	dir := t.TempDir()

	sctx := &pipeline.StepContext{
		WorkDir: dir,
		Config:  &config.Config{},
	}

	if _, ok := loadPRTemplate(sctx); ok {
		t.Fatal("expected no template to be found when no .github template file exists")
	}
}

func TestLoadPRTemplate_NoAutoDetectWithNilConfig(t *testing.T) {
	dir := t.TempDir()
	githubDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(githubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(githubDir, "pull_request_template.md"), []byte("## Summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sctx := &pipeline.StepContext{
		WorkDir: dir,
		Config:  nil,
	}

	tmpl, ok := loadPRTemplate(sctx)
	if !ok || tmpl == nil {
		t.Fatal("expected auto-detection to work even with a nil Config")
	}
}

func TestLoadPRTemplate_ExplicitTemplateTakesPrecedenceOverAutoDetect(t *testing.T) {
	dir := t.TempDir()
	githubDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(githubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(githubDir, "pull_request_template.md"), []byte("auto-detected content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom-template.md"), []byte("explicit content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sctx := &pipeline.StepContext{
		WorkDir: dir,
		Config:  &config.Config{},
	}
	sctx.Config.PR.Template = "custom-template.md"

	tmpl, ok := loadPRTemplate(sctx)
	if !ok {
		t.Fatal("expected explicit template to be found")
	}
	rendered, err := renderPRBodyFromTemplate(tmpl, "", "", "", "", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "explicit content" {
		t.Fatalf("expected explicit template content, got:\n%s", rendered)
	}
}

// Note: a test asserting the lowercase variant wins when both exist isn't
// portable - on a case-insensitive filesystem (macOS APFS default, the CI
// runner's platform), .github/pull_request_template.md and
// .github/PULL_REQUEST_TEMPLATE.md collide onto the same inode, so writing
// both just overwrites one with the other. The candidate order in
// autoDetectPRTemplatePaths (lowercase checked first) still matches
// GitHub's own documented convention on case-sensitive filesystems.

func TestContainsTemplateAction_DetectsGoTemplateSyntax(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"has action", "# {{.Title}}\n\nbody", true},
		{"heading only, no action", "## Description\n\nfill me in\n", false},
		{"empty", "", false},
		{"literal braces but not doubled", "use { or } in code\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsTemplateAction(tc.raw); got != tc.want {
				t.Fatalf("containsTemplateAction(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestMatchPRTemplateHeadingField_MatchesKnownSynonyms(t *testing.T) {
	cases := []struct {
		heading string
		want    string
	}{
		{"## Description", "Intent"},
		{"## Summary", "Intent"},
		{"## Overview", "Intent"},
		{"## Changes", "WhatChanged"},
		{"## What Changed", "WhatChanged"},
		{"## Key Changes", "WhatChanged"},
		{"## Testing", "Testing"},
		{"## Test Plan", "Testing"},
		{"## Tests", "Testing"},
		{"## Risk", "Risk"},
		{"## Risk Assessment", "Risk"},
		{"## Risks", "Risk"},
		{"## Jira", "JiraTicket"},
		{"## Ticket", "JiraTicket"},
		{"## Jira Ticket", "JiraTicket"},
		{"## Issue", "JiraTicket"},
		{"## Pipeline", "Pipeline"},
		// case-insensitive and punctuation-tolerant
		{"## description:", "Intent"},
		{"##   TESTING  ", "Testing"},
		// unmatched
		{"## Affected Page(s)", ""},
		{"## Checklist", ""},
	}
	for _, tc := range cases {
		t.Run(tc.heading, func(t *testing.T) {
			if got := matchPRTemplateHeadingField(tc.heading); got != tc.want {
				t.Fatalf("matchPRTemplateHeadingField(%q) = %q, want %q", tc.heading, got, tc.want)
			}
		})
	}
}

func TestSectionBodyIsBlank_StripsHTMLCommentsBeforeChecking(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"only comment", "\n<!-- Brief description of the change -->\n\n", true},
		{"empty", "", true},
		{"whitespace only", "\n\n  \n", true},
		{"comment plus real content", "\n<!-- guidance -->\nAdds retry logic\n", false},
		{"placeholder bullet counts as content", "\n<!-- guidance -->\n- \n", false},
		{"multiline comment", "\n<!--\nmultiline\nguidance\n-->\n\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionBodyIsBlank(tc.body); got != tc.want {
				t.Fatalf("sectionBodyIsBlank(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestFillBlankSectionBody_InsertsAfterExistingComment(t *testing.T) {
	body := "\n<!-- Brief description of the change and why it is needed -->\n\n"
	got := fillBlankSectionBody(body, "Adds retry logic to the sync worker.")
	if !strings.Contains(got, "<!-- Brief description of the change and why it is needed -->") {
		t.Fatalf("expected guidance comment preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "Adds retry logic to the sync worker.") {
		t.Fatalf("expected value inserted, got:\n%s", got)
	}
	commentIdx := strings.Index(got, "-->")
	valueIdx := strings.Index(got, "Adds retry logic")
	if commentIdx < 0 || valueIdx < 0 || valueIdx < commentIdx {
		t.Fatalf("expected value to appear after the comment, got:\n%s", got)
	}
}

func TestFillBlankSectionBody_NoCommentPrependsValue(t *testing.T) {
	body := "\n\n"
	got := fillBlankSectionBody(body, "Adds retry logic.")
	if strings.TrimSpace(got) != "Adds retry logic." {
		t.Fatalf("expected value alone in body, got:\n%q", got)
	}
}

func TestFillHeadingOnlyPRTemplate_FillsBlankMatchedSectionsOnly(t *testing.T) {
	raw := "## Description\n" +
		"<!-- Brief description of the change and why it is needed -->\n\n" +
		"## Jira Ticket\n" +
		"<!-- Link to the related Jira ticket -->\n\n" +
		"## Changes\n" +
		"<!-- List the key changes made in this PR -->\n\n" +
		"## Affected Page(s)\n" +
		"<!-- Which page(s) does this PR touch? Check all that apply. -->\n" +
		"- [ ] Frontend\n" +
		"- [ ] Backend\n\n" +
		"## Testing\n" +
		"<!-- How was the change tested? Which scenarios were covered? -->\n"

	data := prTemplateData{
		Intent:      "Adds retry logic to the sync worker.",
		WhatChanged: "- retry on transient errors\n- add backoff",
		Testing:     "- unit tests for backoff",
		JiraTicket:  "PROJ-42",
	}

	got := fillHeadingOnlyPRTemplate(raw, data)

	if !strings.Contains(got, "Adds retry logic to the sync worker.") {
		t.Fatalf("expected Description filled from Intent, got:\n%s", got)
	}
	if !strings.Contains(got, "PROJ-42") {
		t.Fatalf("expected Jira Ticket filled, got:\n%s", got)
	}
	if !strings.Contains(got, "retry on transient errors") {
		t.Fatalf("expected Changes filled from WhatChanged, got:\n%s", got)
	}
	if !strings.Contains(got, "unit tests for backoff") {
		t.Fatalf("expected Testing filled, got:\n%s", got)
	}
	affected := got[strings.Index(got, "## Affected Page(s)"):]
	if strings.Contains(affected, "Adds retry logic") || strings.Contains(affected, "PROJ-42") {
		t.Fatalf("expected Affected Page(s) section untouched, got:\n%s", affected)
	}
	if !strings.Contains(affected, "- [ ] Frontend") || !strings.Contains(affected, "- [ ] Backend") {
		t.Fatalf("expected Affected Page(s) checklist preserved verbatim, got:\n%s", affected)
	}
}

func TestFillHeadingOnlyPRTemplate_NeverOverwritesExistingContent(t *testing.T) {
	raw := "## Description\n" +
		"<!-- Brief description -->\n" +
		"Already written by a human.\n\n" +
		"## Testing\n" +
		"<!-- How was it tested -->\n"

	data := prTemplateData{
		Intent:  "Agent-generated intent that must not appear.",
		Testing: "- agent testing notes",
	}

	got := fillHeadingOnlyPRTemplate(raw, data)

	if !strings.Contains(got, "Already written by a human.") {
		t.Fatalf("expected existing human content preserved, got:\n%s", got)
	}
	if strings.Contains(got, "Agent-generated intent") {
		t.Fatalf("expected existing content to not be overwritten, got:\n%s", got)
	}
	if !strings.Contains(got, "agent testing notes") {
		t.Fatalf("expected blank Testing section still filled, got:\n%s", got)
	}
}

func TestFillHeadingOnlyPRTemplate_LeavesSectionBlankWhenFieldValueEmpty(t *testing.T) {
	raw := "## Jira Ticket\n<!-- Link to the related Jira ticket -->\n\n"
	data := prTemplateData{JiraTicket: ""}

	got := fillHeadingOnlyPRTemplate(raw, data)

	if got != raw {
		t.Fatalf("expected template unchanged when field value is empty, got:\n%q\nwant:\n%q", got, raw)
	}
}
