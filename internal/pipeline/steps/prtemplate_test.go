package steps

import (
	"os"
	"path/filepath"
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
