package gate

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/git"
	"github.com/kunchenguid/no-mistakes/internal/paths"
)

// localOriginSuffix distinguishes a local-origin shim from the gate bare repo
// (<id>.git) that shares the repos dir.
const localOriginSuffix = "-origin.git"

// LocalOriginDir returns the path of the local-origin shim for a repo ID: the
// private bare repo `init --local` provisions as the origin of a working repo
// that has no remote at all.
func LocalOriginDir(p *paths.Paths, repoID string) string {
	return filepath.Join(p.ReposDir(), repoID+localOriginSuffix)
}

// IsLocalOrigin reports whether upstreamURL points at a local-origin shim
// under p's repos dir - i.e. whether the repo runs in local mode. This is the
// one marker the rest of the system keys local-mode behavior off (there is no
// schema column), so keep it in sync with LocalOriginDir.
func IsLocalOrigin(p *paths.Paths, upstreamURL string) bool {
	u := filepath.Clean(strings.TrimSpace(upstreamURL))
	if !strings.HasSuffix(u, localOriginSuffix) || !filepath.IsAbs(u) {
		return false
	}
	dir := filepath.Dir(u)
	reposDir := filepath.Clean(p.ReposDir())
	if dir == reposDir {
		return true
	}
	// macOS: the stored path and the current NM_HOME may differ by symlink
	// resolution (/var vs /private/var).
	resolvedDir, errDir := filepath.EvalSymlinks(dir)
	resolvedRepos, errRepos := filepath.EvalSymlinks(reposDir)
	return errDir == nil && errRepos == nil && resolvedDir == resolvedRepos
}

// provisionLocalOrigin wires a working repo that has no origin remote to a
// freshly created local-origin shim: a bare repo whose HEAD symref names the
// working repo's current branch (its default branch from then on), seeded
// with that branch's history, and registered as the working repo's origin.
// Re-running against an existing shim is an idempotent refresh that never
// repoints the shim's HEAD (init may legitimately run from a feature branch)
// and never force-updates its refs. Returns whether a new shim was created.
func provisionLocalOrigin(ctx context.Context, p *paths.Paths, absRoot, id string) (bool, error) {
	shimDir := LocalOriginDir(p, id)

	if existingURL, err := git.GetRemoteURL(ctx, absRoot, "origin"); err == nil {
		if !IsLocalOrigin(p, existingURL) {
			return false, fmt.Errorf("origin remote already exists (%s); --local is only for repositories with no origin remote, run plain `no-mistakes init` instead", existingURL)
		}
		// Refresh: repair the remote in case NM_HOME moved, keep everything
		// else as-is. The shim's refs advance through the pipeline's own
		// pushes and the user's `git push origin <default>` sync.
		if _, statErr := os.Stat(shimDir); statErr != nil {
			return false, fmt.Errorf("local origin shim %s is missing; remove the origin remote (`git remote remove origin`) and re-run `no-mistakes init --local` to provision a fresh one", shimDir)
		}
		return false, git.EnsureRemote(ctx, absRoot, "origin", shimDir)
	}

	// The shim's default branch is whatever the main checkout has checked
	// out when local mode is first initialized.
	branch, err := git.CurrentBranch(ctx, absRoot)
	if err != nil {
		return false, fmt.Errorf("detect current branch: %w", err)
	}
	if branch == "HEAD" {
		return false, fmt.Errorf("detached HEAD: check out the repository's default branch before running `no-mistakes init --local`")
	}

	if err := git.InitBare(ctx, shimDir); err != nil {
		return false, fmt.Errorf("create local origin: %w", err)
	}
	cleanup := func() { os.RemoveAll(shimDir) }
	if _, err := git.Run(ctx, shimDir, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		cleanup()
		return false, fmt.Errorf("set local origin default branch: %w", err)
	}
	// Seed the default branch so the rebase base, the push lease, and the
	// trusted-config fetch all have real refs to work against.
	if _, err := git.Run(ctx, absRoot, "push", shimDir, "refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		cleanup()
		return false, fmt.Errorf("seed local origin with branch %q: %w", branch, err)
	}
	if err := git.EnsureRemote(ctx, absRoot, "origin", shimDir); err != nil {
		cleanup()
		return false, fmt.Errorf("add origin remote: %w", err)
	}

	slog.Info("local origin provisioned", "repo_id", id, "shim", shimDir, "default_branch", branch)
	return true, nil
}
