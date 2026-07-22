---
title: Local Mode
description: Run the full validation pipeline against a purely local git repository - no remote, no PR, no CI.
---

Local mode runs the full no-mistakes gate - rebase, review, test, document,
lint, and every fix round - against a repository that has no remote at all.
The run terminates at "checks green, ready for a local fast-forward merge"
instead of a PR.

## Setup

From a git repository with **no `origin` remote**, with your default branch
checked out:

```sh
no-mistakes init --local
```

This provisions a private bare repository (the *managed local origin*) under
the no-mistakes home and wires it up as the repo's `origin`:

- its default branch is the branch you had checked out, seeded with that
  branch's history
- the rebase step fetches its base from it, so the fast-forward guarantee
  works exactly as in remote mode
- the push step pushes validated branches to it under the same force-push
  safety rules
- the PR and CI steps skip, because a filesystem path resolves to no
  supported provider

`--local` cannot be combined with `--fork-url`, and it refuses to run when an
`origin` remote already exists - local mode is only for repositories without
one. Re-running `no-mistakes init --local` (or plain `init`) later refreshes
the gate and leaves the managed origin untouched. `no-mistakes eject` removes
the managed origin along with the gate.

## Driving a run

Nothing changes: commit on a feature branch and run
`no-mistakes axi run --intent "..."` (or push to the `no-mistakes` remote).
A completed run reports `outcome: passed`, which in local mode means every
check is green and the branch is ready to merge - there is no PR anywhere in
the flow. The output's help line carries the merge command:

```sh
# on your default branch
git fetch no-mistakes <branch> && git merge --ff-only FETCH_HEAD
```

This merges the *validated* head - including any fix commits the pipeline
added - not your local branch tip, which may be behind it.

## Keeping the managed origin in sync

The managed origin is the pipeline's rebase base and trusted-config source,
so it must track your real default branch. After each local merge (and after
any commit made directly on the default branch), push it:

```sh
git push origin <default-branch>
```

If you forget, no code is lost: the next run detects that your branch is
built on default-branch commits the origin has not seen and parks with a
finding instead of silently widening the validation scope.

## Known limitation (v1)

Local mode currently skips the CI step entirely, including the final-head
re-verification a hosted CI run provides in remote mode. Fix commits land
mid-pipeline (for example, lint fixes commit *after* the test step already
ran), so a late fix round that breaks an earlier gate is **not re-caught**
before `passed` is reported. A local verify pass replacing the CI monitor is
the planned follow-up. Until then, treat `passed` on a run with late fix
rounds accordingly - re-running the pipeline (or your test command) after
merging is a cheap belt-and-braces check.
