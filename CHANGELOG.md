# Changelog

What changes for a repo that uses aphrollo, release by release: what you will
notice, and what moves by itself. The commit history is the developer's
record; this file is yours. Every release owes a section here, newest first,
and the version in `internal/buildinfo/VERSION` is checked against it.

A repo names the oldest release it accepts in `aphrollo.toml`:

```toml
[aphrollo]
requires = ">=1.4"
```

A Cargo workspace puts the same key in `[workspace.metadata.aphrollo]`. A
binary older than that does not judge the repo. A hook prints one line naming
the version the repo needs and `aphrollo update`, and lets the edit or commit
through unjudged. A command that would write to the repo refuses with that line
and exit 1. A `requires` the binary cannot read is refused the same way, with
the form that works.

## 1.0.0 - 2026-10-02

The first numbered release. Everything before it is described only by the
commit history; from here, every change a consumer can see is a line in this
file.

### What you will notice

- `aphrollo version` prints the version next to the build stamp, for example
  `aphrollo 1.0.0 (ca47dba built 2026-10-02T09:00:00Z)`. A binary built by hand
  with no stamp still prints its version: `aphrollo 1.0.0 (unstamped)`.
- A repo can declare `requires` (above). No repo declares one yet, so nothing
  is refused today.
- A cloud session's assigned branch passes the undercover branch-name check
  when `APHROLLO_ASSIGNED_BRANCH` names it and `CLAUDE_CODE_REMOTE=true`; every
  other branch name stays under the rule. `workspace merge` now sends the PR
  title and number as the merge subject for squash and merge, so
  `Merge pull request #N from <branch>` never lands in history, and the
  undercover check reads the PR title too.
- The gate writes `events.jsonl` beside `gate.log`: one JSON line for each gate
  result, PR opened, merge, CI state after a push, escape and feedback, each
  with its own format number. It carries metadata only, never command or reason
  text. `gate.log` and what `gate stats` and `gate output` read from it are
  unchanged.
- When hosted CI cannot start a job (an Actions billing lock), `workspace
  merge` says `ci unavailable` and refuses without recording a CI escape,
  instead of reading the outage as a red. `ci = auto | local | github` in
  `aphrollo.toml`, and `--ci` on `workspace merge`, choose who judges: `auto`
  falls back to local CI when the hosted jobs never started. `aphrollo ci run`
  judges the lane merged into trunk with the merge gate's own stage, and keeps
  the verdict per tree so an unchanged tree is not run twice.
- A commit you make while a test-map build or a mutation run is going is no
  longer read as a leak by the canary: the post-commit hook records each commit
  it sees, and only a move with no record counts. The gate's fail-first checkout
  cleanup unlinks a junction or symlink instead of deleting through it, and
  refuses, naming the link, when it cannot. A `refs/notes/gate` ahead on the
  remote is merged and the push retried.
- In a Python repo the edit hook runs pytest under the repo's own virtualenv,
  else a python that can import it, and prints a `SKIPPED` line naming the
  reason, never a red, when none can. The commit and merge gates also look for
  that virtualenv in the repo's primary checkout and in the lane being merged,
  since a throwaway merge worktree has none of its own.
- On Windows, `aphrollo install` converges the user PATH so the shim directory
  and the binary directory each appear once, ahead of Git, and `aphrollo gate
  doctor` reports a missing, duplicated or Git-shadowed entry.

### What migrates by itself

Nothing needs doing. No format changed in this release: `events.jsonl` and the
commit record are new files that appear on first use, and every existing state
file, baseline and law is read as before. The user PATH is rewritten once by
the next `aphrollo install`, and rewriting it again changes nothing.
