package cli

import (
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/daemon"
	"github.com/kunchenguid/no-mistakes/internal/gate"
	"github.com/kunchenguid/no-mistakes/internal/safeurl"
	"github.com/kunchenguid/no-mistakes/internal/skill"
	"github.com/spf13/cobra"
)

const banner = `_  _ ____    _  _ _ ____ ___ ____ _  _ ____ ____
|\ | |  |    |\/| | [__   |  |__| |_/  |___ [__
| \| |__|    |  | | ___]  |  |  | | \_ |___ ___]`

func newInitCmd() *cobra.Command {
	var forkURL string
	var local bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize no-mistakes gate for the current repository",
		Long: "Sets up or refreshes a local bare repo as a gate, installs a post-receive hook,\n" +
			"best-effort isolates the gate hook path from shared local git config writes when Git supports `config --worktree`,\n" +
			"adds or repairs the \"no-mistakes\" git remote, and records the repo in the database.\n\n" +
			"Run this from inside a git repository that has an \"origin\" remote.\n" +
			"For a purely local repository with no remote at all, pass --local: a private\n" +
			"bare repo under the no-mistakes home is provisioned and wired up as origin, so\n" +
			"the full pipeline runs locally and PR/CI steps skip.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return trackCommand("init", func() error {
				p, d, err := openResources()
				if err != nil {
					return err
				}
				defer d.Close()

				if cmd.Flags().Changed("fork-url") && strings.TrimSpace(forkURL) == "" {
					return fmt.Errorf("init: --fork-url must not be empty")
				}
				repo, created, err := gate.InitWithOptions(cmd.Context(), d, p, ".", gate.InitOptions{ForkURL: forkURL, Local: local})
				if err != nil {
					return fmt.Errorf("init: %w", err)
				}
				if err := daemon.EnsureDaemon(p); err != nil {
					// Only roll back a gate we created in this run; a re-init
					// must never eject a user's pre-existing gate.
					if created {
						if _, ejectErr := gate.Eject(cmd.Context(), d, p, "."); ejectErr != nil {
							return fmt.Errorf("start daemon: %w, rollback init: %v", err, ejectErr)
						}
					}
					return fmt.Errorf("start daemon: %w", err)
				}

				// Install the agent skill at user level so agents can drive
				// no-mistakes via `/no-mistakes` in any repo. Best-effort: a
				// skill write failure must not undo a successful gate setup.
				_, skillErr := skill.InstallUser()

				w := cmd.OutOrStdout()
				fmt.Fprintln(w, sCyan.Render(banner))
				fmt.Fprintln(w)
				headline := "Gate initialized"
				if !created {
					headline = "Gate already initialized (refreshed)"
				}
				fmt.Fprintf(w, "  %s %s\n", sGreen.Render("✓"), headline)
				fmt.Fprintln(w)
				fmt.Fprintf(w, "  %s  %s\n", sDim.Render("  repo"), repo.WorkingPath)
				fmt.Fprintf(w, "  %s  no-mistakes → %s\n", sDim.Render("  gate"), p.RepoDir(repo.ID))
				remoteURL := repo.UpstreamURL
				if repo.ForkURL != "" {
					remoteURL = safeurl.Redact(remoteURL)
				}
				if gate.IsLocalOrigin(p, repo.UpstreamURL) {
					fmt.Fprintf(w, "  %s  %s %s\n", sDim.Render("remote"), remoteURL, sDim.Render("(local mode: managed local origin, no network remote)"))
				} else {
					fmt.Fprintf(w, "  %s  %s\n", sDim.Render("remote"), remoteURL)
				}
				if repo.ForkURL != "" {
					fmt.Fprintf(w, "  %s  %s\n", sDim.Render("  fork"), safeurl.Redact(repo.ForkURL))
				}
				if skillErr != nil {
					fmt.Fprintf(w, "  %s  %s\n", sDim.Render(" skill"), sYellow.Render("skipped: "+skillErr.Error()))
				} else {
					fmt.Fprintf(w, "  %s  %s %s\n", sDim.Render(" skill"), sGreen.Render("/no-mistakes"), sDim.Render("installed for agents at user level"))
				}
				if legacy := skill.Vendored(repo.WorkingPath); len(legacy) > 0 {
					fmt.Fprintf(w, "  %s  %s\n", sDim.Render("  note"), sDim.Render("vendored skill copy ("+strings.Join(legacy, ", ")+") is no longer needed and can be removed"))
				}
				fmt.Fprintln(w)
				fmt.Fprintf(w, "  %s\n", sDim.Render("Push through the gate with:"))
				fmt.Fprintf(w, "  %s\n", sBold.Render("git push no-mistakes <branch>"))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&forkURL, "fork-url", "", "GitHub fork remote URL to push branches to while opening PRs against origin")
	cmd.Flags().BoolVar(&local, "local", false, "set up local mode for a repository with no remote: provision a managed local origin so the full pipeline runs without any network host")
	cmd.MarkFlagsMutuallyExclusive("fork-url", "local")
	return cmd
}
