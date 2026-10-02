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

## 1.2.0 - 2026-10-03

`aphrollo update` follows releases, and aphrollo steps aside in a repo that
trellis gates.

### What you will notice

- `aphrollo update` moves to the newest release tag (`v<MAJOR.MINOR.PATCH>`),
  not the latest commit on main. It prints the version it moved from and to
  (`aphrollo update: v1.1.0 -> v1.2.0`), or `[skip] already at v1.2.0`. The
  `--branch` flag is gone, since there is no branch to pick; a release tag is
  made when a change that bumps the version merges. A remote with no release tag
  is refused, with the reason.
- The Linux deploy ships the same newest tag, so the shared box and your update
  agree on one version.
- In a repo whose root holds `trellis.toml`, aphrollo says nothing and writes
  nothing: its editor hooks and git hooks exit 0 silently, and its git and cargo
  queue shims pass straight through. trellis and aphrollo never both gate one repo.
- `aphrollo gate doctor` has a new row, "one live gate": it fails when the
  aphrollo hooks and a trellis plugin or hook are wired into the same Claude
  settings, and names the fix (disable the trellis plugin, or run
  `aphrollo gate init --uninstall`).

## 1.1.0 - 2026-10-02

Local CI (`aphrollo ci run`, and `workspace merge` when `ci = local`) is safer and
easier on a shared box.

### What you will notice

- A run no longer changes the box's global toolchains. Installs land in a scratch
  directory of the run's own, removed when it ends: a python venv first on PATH
  (made when a step mentions python or pip; `PIP_REQUIRE_VIRTUALENV=true` always),
  and per-run npm, go and cargo prefixes and caches. Everything applied is printed
  at the start of the run. Per-run caches mean modules download again each run.
- A step that would change the box outside that (`sudo`, a system package manager,
  `pip install --user`, `yarn global`, `gem install` and the like) is refused before
  it runs, naming the step.
- Jobs run one at a time in needs order (as before), every step below normal
  priority (nice and ionice; BELOW_NORMAL_PRIORITY_CLASS on Windows).
  `--ci-jobs N` or `ci-jobs` in `aphrollo.toml` runs N at once, with each job's
  output prefixed by its name.
- `--ci-timeout 45m` or `ci-timeout` sets the per-step limit (30m by default); a
  step that reaches it is named in the result with its limit.
- A step's last line with no trailing newline no longer runs into the next line.

### Known, not fixed

- On Windows a timed-out step's child processes started through Git Bash can outlive
  the kill.

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
- `aphrollo install` no longer shrinks the PATH the agent harness gives agent
  Bash and every hook. An install run from a minimal PATH (a provisioning tool's
  non-login shell) used to overwrite it, and `go`, `node` and `cargo` fell off.
  The agent PATH is now the shim directory, the entries already there, the
  installing shell's own PATH, then each per-user toolchain directory that
  exists and holds an executable (`~/.local/go/bin`, `~/go/bin`, `~/.cargo/bin`,
  `~/.local/bin`, `/usr/local/go/bin`), each entry once.
- A test command that cannot start is no longer a red. When the tool is not on
  the PATH, the edit line reads `SKIPPED (go not on PATH: ...)`, says the code
  was NOT tested, and the gate log records `skipped-tool-missing`; the narrowed
  rerun stays allowed. `aphrollo gate doctor` fails its `agent PATH` check when
  the agent PATH lacks a tool the repo's suites need: go for a Go root, node for
  an npm root, cargo for a cargo root, python for a pytest root that has no
  virtualenv of its own.
- The commit gate's call-site stage guards three tables of command call sites
  in `internal/argvbatch`, not one: a staged change that moves a row of the
  related-runner table or the loop-built table now runs its guard test at
  commit, where before only CI saw it. It acts in a repo that has those tables
  and is skipped in every other.
- A merge made outside `aphrollo workspace merge` (the GitHub web button,
  `gh pr merge`, a merge by hand) is recorded once the local trunk takes it in:
  `events.jsonl` gets a merge event with `by=outside` and an escape of verdict
  `outside-merge`, because no gate judged it. The recording runs in `workspace
  sync` and in the post-merge hook of a repo with an `aphrollo.toml`, a
  `.ratchet` directory or a Cargo `aphrollo` table. A merge the verb made is
  never counted as outside. `aphrollo workspace sync --since <rev>` backfills
  the merges after a commit that were never recorded (`--dry` names them and
  writes nothing); running it twice records each merge once.
- Three more editor hooks: Stop blocks the end of a turn once when a deferred
  run finished red after the session's last hook, with that run's gate line as
  the reason, and allows the next ask; SubagentStop does the same for a
  subagent's checkout; TaskCompleted exits 2 with the failing test names while
  any project of the task's tree has a red last outcome. A running job, a
  green run, a build that compiled, a setup failure, a memory-cap kill, "no
  tests to run" and a run whose every failure timed out are not a red and block
  nothing. `/tdd off` allows all three, and a payload the hook cannot read
  allows.
- The Windows smoke job also runs the install PATH tests. Nothing changes for a
  consumer.

### What migrates by itself

Nothing needs doing. No format changed in this release: `events.jsonl` and the
commit record are new files that appear on first use, and every existing state
file, baseline and law is read as before. The user PATH and the agent PATH are
rewritten once by the next `aphrollo install`, and rewriting them again
changes nothing. The same run (or `aphrollo gate init`) adds the Stop,
SubagentStop and TaskCompleted entries to the editor's hook settings, one
entry each and only if absent; until it runs, those three hooks are not wired.
