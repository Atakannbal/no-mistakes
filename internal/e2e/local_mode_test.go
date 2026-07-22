//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Local mode (Option A, the "local-origin shim"): a purely local git repo -
// no remote, no GitHub, no PR, no CI - runs the full validation pipeline via
// `init --local`, which provisions a managed bare origin under NM_HOME. The
// run terminates at "checks green, ready for local ff-merge".
//
// Test names are deliberately short: they become part of t.TempDir(), which
// hosts NM_HOME, and the daemon's Unix socket path under it must stay within
// the OS socket path limit (104 bytes on macOS).

// TestLocalInit proves `init --local` provisions the shim origin for a repo
// with no remotes, that plain `init` on such a repo points the user at
// --local, and that re-running either form refreshes idempotently.
func TestLocalInit(t *testing.T) {
	h := NewHarness(t, SetupOpts{Agent: "claude", LocalMode: true})
	ctx := context.Background()

	// Plain init on a no-remote repo must fail with the --local hint.
	out, err := h.RunInDir(h.WorkDir, "init")
	if err == nil {
		t.Fatalf("plain init should fail without an origin remote, got:\n%s", out)
	}
	if !strings.Contains(out, "init --local") {
		t.Errorf("plain init error should suggest `init --local`, got:\n%s", out)
	}

	out, err = h.RunInDir(h.WorkDir, "init", "--local")
	if err != nil {
		t.Fatalf("init --local: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Gate initialized") {
		t.Errorf("init --local should report a fresh gate, got:\n%s", out)
	}
	if !strings.Contains(out, "local mode") {
		t.Errorf("init --local should label the remote as local mode, got:\n%s", out)
	}

	// origin now points at the shim under NM_HOME, seeded with main and with
	// its HEAD symref naming main as the default branch.
	originOut, err := h.runGit(ctx, h.WorkDir, "remote", "get-url", "origin")
	if err != nil {
		t.Fatalf("get origin url: %v\n%s", err, originOut)
	}
	shim := strings.TrimSpace(string(originOut))
	wantShim := filepath.Join(h.NMHome, "repos", h.repoID()+"-origin.git")
	if shim != wantShim {
		t.Errorf("origin url = %q, want shim %q", shim, wantShim)
	}
	if headOut, err := h.runGit(ctx, shim, "symbolic-ref", "HEAD"); err != nil {
		t.Fatalf("shim HEAD: %v\n%s", err, headOut)
	} else if got := strings.TrimSpace(string(headOut)); got != "refs/heads/main" {
		t.Errorf("shim HEAD = %q, want refs/heads/main", got)
	}
	workSHA := h.WorktreeRefSHA("main")
	if shimSHA, err := h.runGit(ctx, shim, "rev-parse", "refs/heads/main"); err != nil {
		t.Fatalf("shim main: %v\n%s", err, shimSHA)
	} else if got := strings.TrimSpace(string(shimSHA)); got != workSHA {
		t.Errorf("shim main = %q, want working tip %q", got, workSHA)
	}

	// Both --local and plain re-init refresh without touching the shim.
	for _, args := range [][]string{{"init", "--local"}, {"init"}} {
		out, err := h.RunInDir(h.WorkDir, args...)
		if err != nil {
			t.Fatalf("re-run %v: %v\n%s", args, err, out)
		}
		if !strings.Contains(out, "already initialized") {
			t.Errorf("re-run %v should report an existing gate, got:\n%s", args, out)
		}
	}
	if originOut, err := h.runGit(ctx, h.WorkDir, "remote", "get-url", "origin"); err != nil || strings.TrimSpace(string(originOut)) != shim {
		t.Errorf("re-init must preserve the shim origin, got %q (err %v)", originOut, err)
	}
}

// TestLocalJourney proves the acceptance flow end to end: a no-remote repo is
// `init --local`'d, a feature branch drives through the full pipeline to
// `outcome: passed` with the local-merge help line, PR and CI self-skip, the
// validated branch ff-merges into the local default branch, and the managed
// origin accepts the post-merge sync push.
func TestLocalJourney(t *testing.T) {
	h := NewHarness(t, SetupOpts{Agent: "claude", LocalMode: true})
	ctx := context.Background()
	mustGit := func(args ...string) string {
		t.Helper()
		out, err := h.runGit(ctx, h.WorkDir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if out, err := h.RunInDir(h.WorkDir, "init", "--local"); err != nil {
		t.Fatalf("init --local: %v\n%s", err, out)
	}

	h.CommitChange("feature/local", "feature.txt", "local change\n", "add local feature")
	runOut, err := h.RunInDir(h.WorkDir, "axi", "run", "--yes", "--intent", "add a local feature")
	if err != nil {
		t.Fatalf("axi run --yes: %v\n%s", err, runOut)
	}
	for _, want := range []string{
		"outcome: passed",
		"merge locally",
		"git fetch no-mistakes feature/local && git merge --ff-only FETCH_HEAD",
		"(on main)",
		"never claim a PR",
	} {
		if !strings.Contains(runOut, want) {
			t.Errorf("axi run output missing %q in:\n%s", want, runOut)
		}
	}
	if strings.Contains(runOut, "Open the PR") {
		t.Errorf("local-mode output must not point at a PR:\n%s", runOut)
	}

	run := h.WaitForRun("feature/local", 60*time.Second)
	if run.Status != types.RunCompleted {
		t.Fatalf("run status = %s, want completed", run.Status)
	}
	// PR and CI must self-skip: the shim path resolves to no supported host.
	for _, step := range run.Steps {
		switch step.StepName {
		case types.StepPR, types.StepCI:
			if step.Status != types.StepStatusSkipped {
				t.Errorf("step %s status = %s, want skipped", step.StepName, step.Status)
			}
		}
	}

	// The ff-merge handoff, exactly as the help line instructs.
	mustGit("checkout", "main")
	mustGit("fetch", "no-mistakes", "feature/local")
	mustGit("merge", "--ff-only", "FETCH_HEAD")
	if mainSHA, gateSHA := mustGit("rev-parse", "main"), mustGit("rev-parse", "FETCH_HEAD"); mainSHA != gateSHA {
		t.Fatalf("main after ff-merge = %s, want validated head %s", mainSHA, gateSHA)
	}

	// The sync story: push the merged default branch to the managed origin.
	mustGit("push", "origin", "main")
	shim := mustGit("remote", "get-url", "origin")
	if shimSHA, err := h.runGit(ctx, shim, "rev-parse", "refs/heads/main"); err != nil {
		t.Fatalf("shim main after sync: %v\n%s", err, shimSHA)
	} else if got := strings.TrimSpace(string(shimSHA)); got != mustGit("rev-parse", "main") {
		t.Errorf("shim main after sync = %q, want local main", got)
	}
}

// TestLocalEject proves eject removes the managed origin along with the gate,
// leaving the working repo with no remotes again.
func TestLocalEject(t *testing.T) {
	h := NewHarness(t, SetupOpts{Agent: "claude", LocalMode: true})
	ctx := context.Background()

	if out, err := h.RunInDir(h.WorkDir, "init", "--local"); err != nil {
		t.Fatalf("init --local: %v\n%s", err, out)
	}
	shim := filepath.Join(h.NMHome, "repos", h.repoID()+"-origin.git")
	if _, err := os.Stat(shim); err != nil {
		t.Fatalf("shim missing after init --local: %v", err)
	}

	if out, err := h.RunInDir(h.WorkDir, "eject"); err != nil {
		t.Fatalf("eject: %v\n%s", err, out)
	}
	if _, err := os.Stat(shim); !os.IsNotExist(err) {
		t.Errorf("shim should be removed on eject, stat err = %v", err)
	}
	if out, err := h.runGit(ctx, h.WorkDir, "remote", "get-url", "origin"); err == nil {
		t.Errorf("origin remote should be removed on eject, got %q", out)
	}
}
