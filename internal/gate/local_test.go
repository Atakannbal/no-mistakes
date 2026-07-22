package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitpkg "github.com/kunchenguid/no-mistakes/internal/git"
	"github.com/kunchenguid/no-mistakes/internal/paths"
)

// setupLocalTestRepo creates a git repo with no remotes at all, one initial
// commit, and the given branch checked out. Returns its resolved path.
func setupLocalTestRepo(t *testing.T, branch string) string {
	t.Helper()
	work := filepath.Join(resolveSymlinks(t, t.TempDir()), "work")
	mustRun := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	mustRun("init", "-b", branch, work)
	mustRun("-C", work, "config", "user.email", "test@test.com")
	mustRun("-C", work, "config", "user.name", "Test")
	mustRun("-C", work, "commit", "--allow-empty", "-m", "init")
	return work
}

func setupTestPaths(t *testing.T) *paths.Paths {
	t.Helper()
	p := paths.WithRoot(t.TempDir())
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	return p
}

func gitOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestInitLocalProvisionsShimOrigin(t *testing.T) {
	work := setupLocalTestRepo(t, "trunk")
	p := setupTestPaths(t)
	d := openTestDB(t, p)
	ctx := context.Background()

	repo, created, err := InitWithOptions(ctx, d, p, work, InitOptions{Local: true})
	if err != nil {
		t.Fatalf("init --local: %v", err)
	}
	if !created {
		t.Error("expected a fresh gate")
	}

	shim := LocalOriginDir(p, repo.ID)
	if repo.UpstreamURL != shim {
		t.Errorf("upstream url = %q, want shim %q", repo.UpstreamURL, shim)
	}
	if !IsLocalOrigin(p, repo.UpstreamURL) {
		t.Errorf("IsLocalOrigin(%q) = false, want true", repo.UpstreamURL)
	}
	if repo.DefaultBranch != "trunk" {
		t.Errorf("default branch = %q, want trunk", repo.DefaultBranch)
	}

	// The shim is a bare repo whose HEAD symref names the default branch and
	// whose branch tip matches the working repo's.
	if got := gitOut(t, "-C", shim, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Errorf("shim is-bare = %q, want true", got)
	}
	if got := gitOut(t, "-C", shim, "symbolic-ref", "HEAD"); got != "refs/heads/trunk" {
		t.Errorf("shim HEAD = %q, want refs/heads/trunk", got)
	}
	workSHA := gitOut(t, "-C", work, "rev-parse", "trunk")
	if got := gitOut(t, "-C", shim, "rev-parse", "refs/heads/trunk"); got != workSHA {
		t.Errorf("shim trunk = %q, want working repo tip %q", got, workSHA)
	}

	// The working repo's origin points at the shim, and the normal gate
	// wiring (bare gate repo + no-mistakes remote) is in place.
	originURL, err := gitpkg.GetRemoteURL(ctx, work, "origin")
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	if originURL != shim {
		t.Errorf("origin url = %q, want %q", originURL, shim)
	}
	gateURL, err := gitpkg.GetRemoteURL(ctx, work, RemoteName)
	if err != nil {
		t.Fatalf("get gate url: %v", err)
	}
	if gateURL != p.RepoDir(repo.ID) {
		t.Errorf("gate url = %q, want %q", gateURL, p.RepoDir(repo.ID))
	}
}

func TestInitLocalIsIdempotent(t *testing.T) {
	work := setupLocalTestRepo(t, "main")
	p := setupTestPaths(t)
	d := openTestDB(t, p)
	ctx := context.Background()

	repo, _, err := InitWithOptions(ctx, d, p, work, InitOptions{Local: true})
	if err != nil {
		t.Fatalf("first init --local: %v", err)
	}

	// A refresh run from a feature branch must not repoint the shim's HEAD
	// (the default branch is sticky) and must not fail on the existing origin.
	if out, err := exec.Command("git", "-C", work, "checkout", "-b", "feature/x").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v: %s", err, out)
	}
	refreshed, created, err := InitWithOptions(ctx, d, p, work, InitOptions{Local: true})
	if err != nil {
		t.Fatalf("refresh init --local: %v", err)
	}
	if created {
		t.Error("refresh should not report a fresh gate")
	}
	if refreshed.ID != repo.ID {
		t.Errorf("refresh changed repo ID: %q -> %q", repo.ID, refreshed.ID)
	}
	if refreshed.DefaultBranch != "main" {
		t.Errorf("default branch after refresh = %q, want main", refreshed.DefaultBranch)
	}
	shim := LocalOriginDir(p, repo.ID)
	if got := gitOut(t, "-C", shim, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Errorf("shim HEAD after refresh = %q, want refs/heads/main", got)
	}
}

func TestInitLocalRefusesPreexistingShimWithoutOrigin(t *testing.T) {
	work := setupLocalTestRepo(t, "main")
	p := setupTestPaths(t)
	d := openTestDB(t, p)
	ctx := context.Background()

	repo, _, err := InitWithOptions(ctx, d, p, work, InitOptions{Local: true})
	if err != nil {
		t.Fatalf("init --local: %v", err)
	}
	shim := LocalOriginDir(p, repo.ID)

	// Sever the origin wiring but leave the shim behind, then re-init from a
	// different branch: the fresh-creation path must refuse to adopt the
	// surviving shim rather than repoint its HEAD, seed into it, or remove it.
	if out, err := exec.Command("git", "-C", work, "remote", "remove", "origin").CombinedOutput(); err != nil {
		t.Fatalf("remove origin: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", work, "checkout", "-b", "feature/y").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v: %s", err, out)
	}

	_, _, err = InitWithOptions(ctx, d, p, work, InitOptions{Local: true})
	if err == nil {
		t.Fatal("expected error when a stale shim already exists")
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "git remote add origin") {
		t.Errorf("error should carry recovery guidance, got: %v", err)
	}
	if _, statErr := os.Stat(shim); statErr != nil {
		t.Errorf("pre-existing shim must survive the refusal, stat err = %v", statErr)
	}
	if got := gitOut(t, "-C", shim, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Errorf("shim HEAD after refusal = %q, want refs/heads/main", got)
	}
}

func TestInitLocalPlainReinitPreservesLocalMode(t *testing.T) {
	work := setupLocalTestRepo(t, "main")
	p := setupTestPaths(t)
	d := openTestDB(t, p)
	ctx := context.Background()

	repo, _, err := InitWithOptions(ctx, d, p, work, InitOptions{Local: true})
	if err != nil {
		t.Fatalf("init --local: %v", err)
	}

	// A plain re-init keeps working against the shim origin unchanged.
	refreshed, created, err := Init(ctx, d, p, work)
	if err != nil {
		t.Fatalf("plain re-init on local repo: %v", err)
	}
	if created {
		t.Error("plain re-init should refresh, not create")
	}
	if refreshed.UpstreamURL != LocalOriginDir(p, repo.ID) {
		t.Errorf("plain re-init changed upstream to %q", refreshed.UpstreamURL)
	}
}

func TestInitLocalRefusesExistingOrigin(t *testing.T) {
	work := setupTestRepo(t) // has a real origin remote
	p := setupTestPaths(t)
	d := openTestDB(t, p)

	_, _, err := InitWithOptions(context.Background(), d, p, work, InitOptions{Local: true})
	if err == nil {
		t.Fatal("expected error when origin already exists")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("error should name the conflicting origin remote, got: %v", err)
	}
}

func TestInitLocalRefusesForkURL(t *testing.T) {
	work := setupLocalTestRepo(t, "main")
	p := setupTestPaths(t)
	d := openTestDB(t, p)

	_, _, err := InitWithOptions(context.Background(), d, p, work, InitOptions{Local: true, ForkURL: "https://github.com/me/fork"})
	if err == nil {
		t.Fatal("expected error when combining --local with --fork-url")
	}
}

func TestInitLocalRefusesDetachedHEAD(t *testing.T) {
	work := setupLocalTestRepo(t, "main")
	sha := gitOut(t, "-C", work, "rev-parse", "HEAD")
	if out, err := exec.Command("git", "-C", work, "checkout", "--detach", sha).CombinedOutput(); err != nil {
		t.Fatalf("detach: %v: %s", err, out)
	}
	p := setupTestPaths(t)
	d := openTestDB(t, p)

	_, _, err := InitWithOptions(context.Background(), d, p, work, InitOptions{Local: true})
	if err == nil {
		t.Fatal("expected error on detached HEAD")
	}
}

func TestInitNoOriginSuggestsLocal(t *testing.T) {
	work := setupLocalTestRepo(t, "main")
	p := setupTestPaths(t)
	d := openTestDB(t, p)

	_, _, err := Init(context.Background(), d, p, work)
	if err == nil {
		t.Fatal("expected error when no origin remote")
	}
	if !strings.Contains(err.Error(), "init --local") {
		t.Errorf("no-origin error should suggest `init --local`, got: %v", err)
	}
}

func TestIsLocalOrigin(t *testing.T) {
	p := paths.WithRoot(filepath.Join(t.TempDir(), "nmhome"))
	cases := []struct {
		url  string
		want bool
	}{
		{LocalOriginDir(p, "abc123"), true},
		{p.RepoDir("abc123"), false}, // a gate dir, not a shim
		{filepath.Join(t.TempDir(), "elsewhere-origin.git"), false},
		{"https://github.com/user/repo.git", false},
		{"git@github.com:user/repo.git", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsLocalOrigin(p, tc.url); got != tc.want {
			t.Errorf("IsLocalOrigin(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestEjectRemovesLocalOriginShim(t *testing.T) {
	work := setupLocalTestRepo(t, "main")
	p := setupTestPaths(t)
	d := openTestDB(t, p)
	ctx := context.Background()

	repo, _, err := InitWithOptions(ctx, d, p, work, InitOptions{Local: true})
	if err != nil {
		t.Fatalf("init --local: %v", err)
	}
	shim := LocalOriginDir(p, repo.ID)

	if _, err := Eject(ctx, d, p, work); err != nil {
		t.Fatalf("eject: %v", err)
	}
	if _, err := os.Stat(shim); !os.IsNotExist(err) {
		t.Errorf("shim origin should be removed on eject, stat err = %v", err)
	}
	if _, err := gitpkg.GetRemoteURL(ctx, work, "origin"); err == nil {
		t.Error("origin remote pointing at the shim should be removed on eject")
	}
}
