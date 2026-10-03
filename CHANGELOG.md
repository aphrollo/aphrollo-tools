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

## 1.6.4 - 2026-10-03

The guardrail refuses `python -` on a null stdin on Windows.

### What you will notice

- A Bash call that runs `python`, `python3` or `py` with `-` (or no script) as the script while stdin
  is redirected from `/dev/null` or `NUL` is refused with the rule name `python-stdin-null` and its fix.
  Git Bash maps `/dev/null` to a console device python takes for a terminal, so the REPL started and
  spun at 100% CPU until killed. A script file (`python file.py < /dev/null`), `-c`, `-m` and a heredoc
  without the redirect are unaffected, and so is every call off Windows.

## 1.6.3 - 2026-10-03

A timed-out or cancelled suite run ends its whole process tree.

### What you will notice

- A suite the gate gives up on at its deadline no longer leaves `<pkg>.test.exe` stuck in kernel exit
  holding commit charge, nor an MSYS shell's grandchildren that `taskkill /T` could not reach: the
  runner starts inside a job object that kills on close (a process group on Linux), and the whole tree
  ends with it. This covers the edit hook's suite, the commit gate's suite and the read-only
  `git`, `go list`, `cargo metadata` and `gh` calls the suite package makes.
- Verdicts, output text, timeouts and gate lines are unchanged. The memory cap still reports
  `OOM-KILLED at <n> GB` as inconclusive. A read-only call that had no time limit now ends after ten
  minutes instead of hanging.
- A box that will not give a suite child its job object or process group no longer turns that into a
  red: the child runs without the guard, keeps its timeout, and the gate says so on stderr.
- A read-only `git`, `gh`, `go list` or `cargo metadata` call never prompts on a terminal; it fails at
  once instead of waiting.

## 1.6.2 - 2026-10-03

A commit through the gate keeps its staged files.

### What you will notice

- A commit in a lane no longer lands empty. Since 1.5.4 the gate's checkout of HEAD for the
  fail-first proof inherited the index the commit was building and reset it, so git recorded an
  empty commit and the proof logged `inconclusive (fail-open)`. The gate now hands that index only
  to the git calls that read the staged tree (`ls-files`, `diff`, `diff-index`, `show`, `cat-file`,
  `write-tree`); a call that checks a tree out or writes an index never receives it. `git commit -a`
  and `git commit <paths>` are still judged on the index they build.

## 1.6.1 - 2026-10-03

Two Windows flakes in the gate's own state writes are fixed at the cause.

### What you will notice

- A state file the gate publishes by rename (a deferred job's result, the session state) is read
  with delete sharing on Windows and replaced with a POSIX-semantics rename, so a reader that
  polls the file no longer holds the writer off until it fails with `Access is denied`. A reader
  that opens the file without delete sharing is still waited out for up to a second.
- An event written without a sequence number because another writer held the log's lock past
  100 ms is numbered when the log is read, from its place in the file, so `ReadEvents` never
  returns two records of a month log with one sequence number, or a zero.

## 1.6.0 - 2026-10-03

`aphrollo stats` reports how the pipeline is doing, from the event log it already keeps.

### What you will notice

- `aphrollo stats [--repo <path>] [--lane <name>] [--week | --since <dur>] [--json]` prints, for the
  current repo, how long a lane takes from its first event to its merge (p50 and p90), how many
  lanes were green on the first CI run and why the others were not, the gate's wall time per lane,
  how many runs proved nothing and why, how long a verdict takes after an edit, how many edits
  each message brings, and the denies, overrides, wrong blocks and escapes. It only reads.
- A deny that was waived within ten minutes on the same lane counts as a wrong block.
- A gate timing that is negative or longer than a day is left out of the gate time and counted on
  its own line, so one broken clock reading cannot swamp the sum.
- `aphrollo stats --briefs` measures the managed CLAUDE.md block, the tdd skill and each agent
  brief at bytes divided by four and marks the ones over their cap: 400 tokens for the block and
  the skill, 250 for an agent.
- `aphrollo gate stats` is unchanged and still reports gate.log.

## 1.5.4 - 2026-10-03

The commit gate judges what `git commit -a` and `git commit <paths>` stage.

### What you will notice

- `git commit -a` and `git commit <paths>` are no longer refused by the commit gate with
  `Unable to create ... index.lock: File exists`. The gate now reads the staged tree from the index
  the commit is building, so the checkout the dependency-graph laws query, fail-first and the
  mutation diffs all see what the commit will hold.
- A write the harvest polls on Windows retries a rename that a reader's open handle blocked for up
  to a second, instead of failing with `Access is denied`.

## 1.5.3 - 2026-10-03

A mutation survivor a repo only reports is no longer logged as a refusal.

### What you will notice

- With `mutants-at-commit` set but not `"block"`, a commit with survivors goes through and the gate
  log now says `mutants-reported:tested=...` where it said `mutants-refused:tested=...`. The same
  holds for the merge measurement under `mutants-at-merge-level`. `mutants-refused:` is written
  only where the commit or merge was refused: a pinned `block`, or a measurement that broke.
- `aphrollo gate stats` counts the reported form under its own `reported` reason in the mutation
  stage line, not as a red, and the post-merge retro no longer counts it as a gate refusal.

## 1.5.2 - 2026-10-03

The event log can no longer slow a hook down, and no longer loses the record after a torn one.

### What you will notice

- A log whose lock file cannot be opened costs one quick failure per process instead of two
  seconds per event, and prints nothing about a build lock. Its events are written unnumbered. A
  wait for a lock another writer holds is capped at 100 ms, down from 2 s.
- A record written after a line a crash tore starts on a line of its own, so it is read. Every
  record now has a blank line before it; readers skip blank lines.
- On Linux and macOS a `LOCALAPPDATA` variable no longer moves the state root, and a relative
  `TRELLIS_DATA` is taken relative to where the hook ran from, once, as an absolute path.
- Recording a settled CI result looks for an earlier record in this month's and last month's log
  only, so a long-lived log does not make each push read its whole history.

## 1.5.1 - 2026-10-03

A test run no longer sees the CI runner's variables.

### What you will notice

- The environment the gate seals a test or mutation run to drops `CI`, `GITHUB_*`, `RUNNER_*` and
  `ACTIONS_*`, whichever case the box spells them in. A test that read `GITHUB_SHA` passed in CI and
  failed on a box that has none; it now sees the same environment in both places.
- `aphrollo ratchet check` counts a new law, `exec_outside_run`: child processes started outside
  `internal/run`, the package that ends a child's whole tree when it is closed, times out or exits.
  Today's sites are the baseline and only go down.

## 1.5.0 - 2026-10-03

The gate keeps its event log per repository, outside the plugin, and records what the pipeline
measures itself by.

### What you will notice

- Events now go to `<state root>/state/<repo>/events-YYYY-MM.jsonl`, one file per repository and
  month. The state root is `$TRELLIS_DATA`, else `%LOCALAPPDATA%\trellis` on Windows, else
  `${XDG_STATE_HOME:-~/.local/state}/trellis`. A repo and all its worktrees share one log. The
  older `events.jsonl` beside `gate.log` is no longer written; what it holds is still read.
- Each record carries a format version and a sequence number that grows with the file, and a reader
  skips a record of a version it does not know, so two binaries of different ages can share a log.
  Two processes appending at once never lose or tear a record.
- New in the log: how long each hook took (`hook.timing`, which also marks where each message
  begins), every edit, every refused edit with the rule, its family (smell, law or wall) and the
  override it offered, every override used, every run that proved nothing with its cause (timeout,
  skipped, queued, deferred, infra), and how long after an edit its verdict arrived. An escape
  records whether it was a product escape, the gate disagreeing with itself, or a canary firing,
  and a first CI run that went red records whether a test, the mutation check or something else
  failed.
- Nothing needs doing. Nothing in a repo changes; the log is local and holds no file contents.

## 1.4.7 - 2026-10-03

`workspace ship` resumes a lane that is already committed.

### What you will notice

- `aphrollo workspace ship` with nothing to commit prints `[skip] nothing to commit` and goes on to
  push and open the PR, and no longer needs `-m` then. With changes to commit it still requires a
  message. `workspace commit` prints the same `[skip]` prefix on a clean tree.


## 1.4.6 - 2026-10-03

A PR opened with a body keeps it, and the version check judges a branch by its own change.

### What you will notice

- `aphrollo workspace pr --body "..."` (and `ship`, and `submit` with a summary) opens the PR with
  the body you gave. Before, a body given without `--title` was replaced by the commit list the
  title was filled from, so the PR opened without its `version:` line.
- `aphrollo version check` measures VERSION and the changed files from the merge base of `--base`
  and HEAD, so a branch that is behind its base is no longer blamed for the base's own version
  bump. The pipeline's `version-check` job checks out the PR head for the same reason.
- The `windows-smoke` workflow is gone: `test-windows` runs the full suite on Windows, which
  covers what the smoke slice ran. Nothing in a consuming repo changes.

## 1.4.5 - 2026-10-03

Merging trunk into a lane branch no longer runs the full test suite.

### What you will notice

- A `git merge main` (or `origin/main`) inside a lane is not the tree that lands on trunk, and CI
  tests the lane after the push, so the pre-merge-commit gate runs only the cheap stages on it:
  the mutation configuration, the staged-baseline guard, the laws and the doc citations. It prints
  `gate premerge: catch-up merge of main into lane/x — suites skipped, CI tests the lane`. A merge
  into trunk, and a merge of one lane into another, run the suites as before.
- A branch that is merely named `master` beside a trunk of another name is no longer asked about
  when the gate works out which commit a lane diverged from; the repo's own trunk (the remote's
  default branch, else `init.defaultBranch`) and its local branch are. A repo whose trunk really is
  `master` is unchanged.

## 1.4.4 - 2026-10-03

A push to `main` no longer runs the test suites a second time on a tree the pull
request's own CI already tested green.

### What you will notice

- When a pull request merges and the commit on `main` holds the tree its pipeline
  run tested, the `test`, `test-windows`, `gate-env`, `lint` and `benchmarks` jobs
  are skipped on the push; `release` still tags the merge. The `changes` job's
  summary names the pull request and run whose verdict was reused.
- Any doubt runs the full suite as before: trunk moved so the trees differ, a check
  not green, a re-run attempt, a run that is not `pipeline.yml` from this
  repository, or no pull request for the commit. `scan` always runs.

## 1.4.3 - 2026-10-03

An edit to a Go test file runs that file's package, not the directory's whole subtree.

### What you will notice

- Editing a test file in `internal/tdd` used to run `go test ./internal/tdd/...`, every subpackage's
  suite, for one edit; it now runs `go test ./internal/tdd`, the package and its external test
  package. A package with subpackages (`internal/cli`, `internal/ratchet`) gets the same cut.
- A run that is already going for the identical request (same checkout, command, HEAD and tree
  state, process alive) is not started a second time by another hook or session. The edit's line
  reads `BUILDING (deferred; ... the identical run for this tree state is already running, not
  started again; ...)`, and `aphrollo gate status --wait` waits on the run that is going.

## 1.4.2 - 2026-10-03

Release tags no longer wait on the deploy host.

### What you will notice

- A release tag is created as soon as a merge's checks pass, even while the
  self-hosted deploy host is down. Before, a queued deploy held the pipeline run
  open, later runs were cancelled, and versions 1.2.1, 1.3.0 and 1.3.1 were never
  tagged, so `aphrollo update` found nothing new.
- The deploy runs from its own workflow, dispatched by the release step; a newer
  release replaces a deploy still waiting for the host.

## 1.4.1 - 2026-10-03

The merge gate is stricter about which CI checks it will take in place of a local run.

### What you will notice

- A re-run of a CI job is never taken for the tree CI tested: a re-run keeps the merge
  commit it first tested but starts later, so its start time cannot say which merge it
  saw. The gate reads each check's workflow run and reuses only first attempts; a run it
  cannot read counts as not reusable, and the full local gate runs. The line
  `CI's verdict is not reused (<why>)` says so.
- Only the checks the repo's own workflow published count: the app must be
  `github-actions` and the workflow file `pipeline.yml`. A check of the same name from
  another app or workflow no longer stands in for the suites, and if the bound checks
  do not exist the gate runs locally.
- A repo whose jobs are named otherwise lists them under `[aphrollo]` in `aphrollo.toml`:
  `ci-reuse-checks = ["unit", "unit-windows"]` (the first is required, the others count
  when present) and `ci-reuse-workflow = "ci.yml"`.
- When the diff changes only markdown, CI's test job passes with its steps skipped; the
  gate still reuses it and says `CI ran no tests: non-code diff` on the reuse line.

## 1.4.0 - 2026-10-03

The merge gate takes CI's verdict for a tree CI has already tested, instead of
running the whole suite again on your box.

### What you will notice

- `workspace merge` on GitHub's green no longer re-runs the suites, vet and lint
  locally when the tree it would test is the tree CI tested: the PR head already
  holds trunk, or a merge of the head onto trunk gives the same tree as GitHub's
  merge ref, built no later than the checks began. The gate prints `reused CI
  verdict for tree <oid> (linux, windows)`, naming each OS whose check passed: the
  `test` job for Linux, and the `test-windows` shards where the pipeline has them.
- The merged tree is still judged for its ratchet laws, baselines and doc
  references; only the suites, vet and lint are taken from CI.
- Anything short of that runs the full local gate as before: trunk moved on to a
  different tree, a check missing or not green, checks for another commit than the
  one being merged, or a repo that measures mutants locally at the merge. The line
  `CI's verdict is not reused (<why>)` says which.
- A repo turns this off with `ci-reuse = false` under `[aphrollo]` in
  `aphrollo.toml`. A value other than `true` or `false` is refused naming the key.

### Known, not fixed

- When trunk has moved, the fallback is the full local run; it is not yet cut down
  to the packages where trunk's change and the PR's change meet.

## 1.3.1 - 2026-10-03

CI now runs the whole test suite on Windows too.

### What you will notice

- A new `test-windows` job in the pipeline runs `go test -race -count=1 -shuffle=on`
  on a GitHub-hosted Windows runner, split into five shards (the mutation package,
  the cli package, the `internal/tdd` root, its subpackages, and everything else) so
  no shard holds two of the packages that outrun a single run's time cap.
- The job is not a required check yet; the repo owner adds it in branch protection.
  Nothing else changes for a repo that uses aphrollo.

## 1.3.0 - 2026-10-03

Mutation testing is opt-in and reports. A repo that declares nothing runs no
mutation at commit or at merge; a repo that opts in gets a report, and a
survivor refuses a commit or a merge only where the repo pins `block`.

### What you will notice

- A repo with no mutation key runs no mutation anywhere and is refused nothing.
  This was already so for the keys below being absent; it is now the stated rule.
- Opting in keeps its spellings: `mutants-at-commit = true` and
  `mutants-at-merge = "ci"` (or `true`) still mean opted in, and now at the
  report level. `mutants-at-commit = "report"` means the same as `true`.
- The commit-time run (`mutants-at-commit`) mutates the lines a commit adds
  inside 90 seconds by default (it was 60; `mutants-commit-budget` still sets
  it), names each survivor and each mutant it did not reach (`NOT MEASURED`), and
  lets the commit through. `mutants-at-commit = "block"` pins the old refusal.
- Under `mutants-at-merge = "ci"`, CI's `mutants-verdict` check passes and prints
  the survivors under `REPORT ONLY`, and the merge gate no longer waits for the
  check or refuses on it. `mutants-at-merge-level = "block"` pins the old
  behaviour: a survivor fails the check, and the merge gate refuses a merge whose
  check did not pass. A measurement that is itself broken (a missing shard
  report, an accept-list that cannot be read) still fails the check. GitHub's
  branch protection is not changed by this release; a repo that listed
  `mutants-verdict` as a required check removes it itself.
- `mutants-before-pr = true` follows the same level: `workspace pr`, `ship` and
  `submit` print survivors under `REPORT ONLY` and open the PR, unless the repo
  pins `mutants-at-merge-level = "block"`.
- A repo that pins neither level no longer gets the "quote one `mutants prove`
  KILLED line per new condition" and loop-index rules in its managed CLAUDE.md
  block; it gets one line saying findings are guidance. A repo that pins `block`
  keeps both rules. Run `aphrollo install --managed-block-only` to refresh the
  block.
- `aphrollo install` and `aphrollo config` list the new `mutants-at-merge-level`
  key.

### What moves by itself

- A repo that declared `mutants-at-commit = true` or `mutants-at-merge = "ci"`
  and relied on a survivor being refused now sees it reported instead. Pin
  `block` to keep the refusal.
- This repo (aphrollo-tools) is opted in at the report level and pins no block.

## 1.2.1 - 2026-10-03

Local CI runs are safer on a shared box and say more about what they did not run.

### What you will notice

- A step the run refuses to execute (`sudo`, a system package manager,
  `pip install --user` and the like) is now listed as skipped with the reason, like
  a `uses:` step, instead of failing the job. It is never tolerated into a green by
  `continue-on-error`. A run whose only trouble is such a step is neither green nor
  red: the merge gate records it as inconclusive, names the steps, and stores no
  green for that tree.
- The python venv is made whenever python is on PATH, not only when a step names
  python or pip, so a script such as `./ci.sh` that pip-installs lands in it.
- `shell: python -u {0}` and other templates that start with python run with the
  run's venv python and are not scanned as shell.
- pipx, uv and rustup now install inside the run's scratch too (`PIPX_HOME`,
  `PIPX_BIN_DIR`, `UV_TOOL_DIR`, `UV_TOOL_BIN_DIR`, `UV_PYTHON_INSTALL_DIR`,
  `UV_CACHE_DIR`, `RUSTUP_HOME`). The run's `RUSTUP_HOME` links the toolchains the
  box already has and copies its `settings.toml`, so cargo and rustc keep working;
  a toolchain or update the run downloads goes with the scratch.
- Stopping a merge with Ctrl-C or a kill now cancels the run's steps and removes the
  run's scratch directory; `aphrollo gate gc` sweeps `aphrollo-ci-run-*` left by a
  run that was killed outright. A read-only module cache no longer keeps a scratch
  directory from being removed on any platform.
- The scan for global installs reads more of what a step runs: an escaped quote in a
  double-quoted string, `timeout`, `nice`, `command`, `stdbuf` and `xargs` with their
  flags, the script given to `sh -c` or `bash -c`, a here-document fed to a shell,
  and `py -3 -m pip install --user`.
- A step's timeout is no longer reported against a later step that fails before it
  runs.

### Known, not fixed

- A flag or variable a workflow sets itself to point an install at the box
  (`GOBIN=...`, `--prefix`, `--root`, `pip --isolated`, `npm config set`) is not
  scanned for, nor are credentials in a cargo config, nor the cost of the per-run
  caches.
- A component or target added to a toolchain the box already has writes through the
  link into the box's copy.

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
  It never downgrades: when the newest tag is older than the running version it
  prints `[skip] newest tag vX is older than the running vY` and exits 0.
- The Linux deploy ships the same newest tag, so the shared box and your update
  agree on one version.
- In a repo whose root holds `trellis.toml`, aphrollo says nothing and writes
  nothing: its editor hooks and git hooks exit 0 silently, and its git and cargo
  queue shims pass straight through. trellis and aphrollo never both gate one repo.
- `aphrollo gate doctor` has a new row, "one live gate": it fails when the
  aphrollo hooks and a trellis plugin or hook are wired into the same editor
  settings, and names the fix (disable the trellis plugin, or run
  `aphrollo gate init --uninstall`).

## 1.1.1 - 2026-10-03

Edits are lighter on the box, and a test run that waits for a build slot is no
longer lost.

### What you will notice

- An edit now leaves one background job at most, the test build. The background
  lint and mutation runs that followed every edit are gone: lint and mutation
  are judged when you commit, and in CI.
- A test run queued behind another build keeps its place for as long as the run
  may live, and shows as BUILDING meanwhile. Before, it gave up after a quarter
  of that time and the next hook reported "the code was NOT tested".
- The gate's record of where a session stood now follows the lane's own git
  index, so it notices changes in a linked worktree (before, it read as unchanged
  in every lane).
- The law scan skips the `.git` file of a linked worktree and `.claude/worktrees/`,
  so a nested checkout's files are no longer judged as part of the repo around it.

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
