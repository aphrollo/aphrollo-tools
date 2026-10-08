# aphrollo-tools

One Go binary, **`aphrollo`**, that moves deterministic developer work out of
the agent token stream: LSP-backed rename and navigation, git worktree
lifecycle, the dev-tier control plane, and the TDD + law gates.

Every verb is **lossless**, **deterministic** and **idempotent** (done work
reports `[skip]`). Every mutating verb (`workspace`, `refactor`, `sqlc regen`,
`gate gc`, `gate probe discard`, `gate split-commit`, `gate install`, `update`,
`ratchet init`, `ratchet check`, `gate mutants hold`, `issue`, `gate feedback`,
`gate escape record`) executes by default and `--dry` previews; `--apply` is a
legacy no-op that prints a notice; `dev` acts immediately.

**The reference is the tool itself:** `aphrollo <verb> --help`. This file only
maps the verbs.

## Install

```sh
go build -o aphrollo ./cmd/aphrollo   # then put it on PATH
aphrollo install                      # session hooks, git gate, skills, agents, queue shims
aphrollo install --managed-block-only --repo <lane>   # re-render only the CLAUDE.md block (no hooks, no shims)
aphrollo version                      # the release version (0.0.0-dev+sha for a dev build), then the stamped commit and build time
aphrollo update                       # build the newest release tag into ~/.aphrollo/bin/<version> (no root); --to <version> switches back
```

A version comes from the release tag a binary is built at, never from a file in
the source: `aphrollo update` stamps the tag it builds into the
binary, and a build at no release tag reports `0.0.0-dev+<sha>`. PATH names
no version: install puts the queue shim dir and the root (`~/.aphrollo/bin`,
`%LOCALAPPDATA%\aphrollo\bin` on Windows) first, whose `aphrollo` launcher
follows `current`, so an update reaches every open shell at once. A PR does not
carry a number. It says `version: none|patch|minor|major` in its body and, when
it is not `none`, adds one `changelog.d/<lane>.md` fragment (a first line
`level: patch|minor|major`, then what a consumer will notice, in plain words;
see `changelog.d/README.md`). When a push to `main` carries fragments the newest
`v*` tag does not contain, the release job tags the next version (the highest
level among them, bumped from that tag) and creates a GitHub Release whose notes
are those fragments; `aphrollo release plan` prints the tag it would make, and
`aphrollo changelog` prints the whole history, assembled from the fragments each
tag first contains above `CHANGELOG.md`, which is the frozen record of the hand-written releases.
A PR body may also say what the change should move, one `expect: <metric> <p50|p90|rate> <down|up>` line each (for example `expect: merge-queue p50 down`; the metrics are `edit-suite`, `edit-to-verdict`, `commit-gate`, `commit-gate-total`, `merge-gate`, `merge-gate-total`, `mutation-commit`, `merge-queue`, `ci-pipeline`, `pr-lead-time`, `declared-reuse-saved` with `p50` or `p90`, and `wrong-blocks` with `rate`). `workspace pr` and `submit` refuse a line that names another metric or is malformed, and name the valid ones. The merge records the lines with the newest release tag; once 30 lanes ran on a newer binary, `aphrollo report` has an Expectations section with the before and after values and the Mann-Whitney verdict of the speed section (`~` is no clear change), and a line that did not hold is a proposal. Until then the line reads `pending (n of 30 lanes)`.
A repo declares the oldest binary it
accepts with `requires = ">=1.4"` under `[aphrollo]` in `aphrollo.toml` (under
`[workspace.metadata.aphrollo]` in a Cargo workspace's manifest). An older
binary does not judge that repo: a hook prints one line naming the version
needed and `aphrollo update`, and lets the edit or commit through; a verb that
writes repo state or judges the tree by its laws (`ratchet`, `check`, `docs`,
`sqlc`, `install`, `workspace` bar `list`, and the like) refuses with exit 1 and
the same line. A dev build has no version to compare, so it judges as ever and
prints one line saying the repo's minimum went unchecked.
`aphrollo version check --base <ref> --body-file <file>` holds a change to the
version rule in this repo's CI: the body line, the one fragment and its level,
no edit to a released changelog section or a merged fragment.

A shell that still resolves `aphrollo` to an older install (a root-owned `/usr/local/bin/aphrollo`, say) hands `workspace`, `ratchet`, `ci` and the `gate` hooks to the user-space `current` when that names a strictly newer release the same account owns: the same argv, stdin, environment and exit code, and for every verb but the `gate` hooks one stderr line first, `aphrollo <self> -> <newer> (<path>): a newer install runs this` (printed once the newer binary has started; on Unix just before the exec, which cannot be undone). `version`, `update`, `install` and help text are never handed off. The child carries `APHROLLO_HANDOFF=<its version>`, and only a guard equal to the version about to be handed to stops a handoff, so a stale or leaked guard never hides a different, newer install. A pointer that is unreadable, not MAJOR.MINOR.PATCH or missing its binary, a binary owned by another account, or a version that is equal or older, runs this binary silently; `APHROLLO_NO_HANDOFF=1` switches the handoff off. The newer binary is not probed first: if it crashes at start, its non-zero exit and stderr pass through (the hooks call that same binary directly, so it would fail them too). `aphrollo version` prints `newer install: <ver> at <path>` when one exists.

`refactor`, `find`, `outline` and `show` need the language server on PATH:
`gopls`, `rust-analyzer`, `pyright-langserver` or `typescript-language-server`.

## Verbs

| verb | does |
|---|---|
| `aphrollo refactor` | rename a symbol across a project (LSP) |
| `aphrollo find` | references to a symbol |
| `aphrollo outline` | a file's symbols |
| `aphrollo show` | one symbol's source |
| `aphrollo workspace` | lanes (worktrees) and their git verbs: `create`, `commit`, `push`, `pr`, `submit`, `ship`, `merge [--wait <pr>... | --wait --resume]`, `prune`, `diff`, `status` |
| `aphrollo dev` | dev-tier units: `up`, `down`, `restart`, `status`, `logs` |
| `aphrollo guardrail` | PreToolUse policy for long foreground waits, noisy commands and a python REPL on a null stdin (Windows); `gate pretooluse` runs it too, so the installed hook enforces it |
| `aphrollo gate` | the TDD + law gates: hook entry points, `status`, `stats`, `output`, `allow`/`revoke`, `mutants`, `escape`, `probe discard`, `split-commit`, `gc`, `classify-diff` |
| `aphrollo ratchet` | the law engine: `check`, `test`, `init`, `presets`; `check --adopt <law>` writes a new or widened law's first baseline |
| `aphrollo docs` | `docs check`: every repo path a tracked `*.md` cites must resolve |
| `aphrollo sqlc` | `check` for sqlc drift, `regen --scoped` |
| `aphrollo check` | judge the tree read-only: ratchet, docs, sqlc, doctor |
| `aphrollo stats` | the pipeline measures folded from the repo event log: lane speed, first-run CI by cause, gate wall time, not-tested runs, edit-to-verdict latency, denies, overrides, wrong blocks, escapes (`--repo`, `--lane`, `--week`, `--since`, `--json`); `--briefs` measures the managed block, the skill and each agent brief against the token caps; `--shadow` prints what the trellis kernel would have decided beside the live hooks, per rule ; `--ab` prints the red-to-green A/B per arm and language: lanes, denies, warnings, overrides, escapes, friction and whether each arm has 30 lanes |
| `aphrollo report` | the weekly improvement report from the event log: friction per rule (time the agent waited apart from time the gate ran in the background), speed (p50, p90 and max seconds per gate stage, the merge queue and PR lead time, with the p50 change against the week before and between the binary versions of the window, shown only when both sides have four runs and a Mann-Whitney U test gives p under 0.05, else `~`; a version is confounded with the work done that week, so read a version split as a pointer, not a cause; runs of no binary version are left out of the split and a first version with fewer than four runs stays on its own), a leading Changed block of what moved (clear speed changes, rules new and gone, rising not-tested and wrong-block counts), wrong-block candidates, escapes with the stage that should have caught them, the A/B and shadow per arm and language, token cost, and proposals, each with the event seqs behind it and the session usage from the harness's local transcripts, aggregates only (`--repo`, `--since 7d`, `--compare-at <date|sha>`, `--json`); `--issue` opens one `Report <ISO week>` issue and closes last week's, and once, the first time both arms hold 30 lanes, an `A/B ready: <repo>` issue (`--dry` previews); the daily gc sweep files it weekly unless `report = false`; `report web [--out <path>] [--no-open]` writes the same report as one self-contained HTML page (a summary against the week before, proposals grouped by change, inline CSS charts, light and dark, readable at phone width, no script, nothing fetched) to `<git common dir>/aphrollo-report/report-<ISO week>.html`, prints the path and opens it in the browser (no server) |
| `aphrollo why` | replays one deny or run result of the repo event log by its seq: the rule, cause, override offered, whether an override followed within 10 minutes, this rule's denies, overrides and wrong blocks, and for a run the verdict, cause and edit-to-verdict latency (`<seq>`, `--repo`, `--json`) |
| `aphrollo ci` | `ci run`: the one CI entry point; runs the repo's own pull_request workflow(s) on this HEAD merged into trunk in a throwaway worktree (run: steps under bash, uses: steps listed and skipped, first matrix combination only, no mutation; installs land in a scratch venv, npm, go, cargo, pipx, uv and rustup directory of the run's own, and a step that would change the box outside them (sudo, a system package manager, pip --user) is listed as skipped by name, which leaves the run inconclusive rather than green); jobs run one at a time in needs order at below-normal priority (`--ci-jobs N`, or `ci-jobs`, runs N at once) and a step is stopped after `--ci-timeout` (`ci-timeout`, 30m by default), naming the step; a green is stored per tree and reused; `ci why [<pr>\|<run-id>\|--main]`: why a pipeline run is red; `ci reuse`: for a workflow's changes job, whether a push or merge queue run may skip the heavy jobs because a green run already tested the same tree (see below) |
| `aphrollo issue` | open an issue against this repo |
| `aphrollo release` | `release plan`: the release tag a push to main owes, from the changelog.d fragments not yet in the newest tag (read-only) |
| `aphrollo changelog` | the full changelog assembled from the fragments each release tag first contains, newest first, above the frozen CHANGELOG.md (read-only); `--tag vX.Y.Z` prints one release's notes |
| `aphrollo feedback` | file gate feedback with the upstream tracker |
| `aphrollo status` | one-line gate state for this checkout |

## Test once in CI: `aphrollo ci reuse`

A pull request is tested on its branch, then again on the merge queue's group,
and a push to main tests the same tree a third time. `aphrollo ci reuse` lets
the later run take the earlier green run's verdict when it tested the very same
tree (a pull request based on the current main), so the heavy jobs stand down.
It is for any repo that uses a GitHub merge queue; the changes job of your
workflow computes the answer and each heavy job reads it:

```yaml
on:
  pull_request:
  merge_group:
  push:
    branches: [main]
jobs:
  changes:
    runs-on: ubuntu-latest
    permissions: { contents: read, actions: read, pull-requests: read }
    outputs:
      reuse: ${{ steps.reuse.outputs.reuse }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      # ... build or download the binary and put it on PATH here ...
      - name: Record the tree this run tested
        if: github.event_name == 'pull_request' || github.event_name == 'merge_group'
        run: mkdir -p tested-tree && git rev-parse HEAD^{tree} > tested-tree/tree
      - uses: actions/upload-artifact@v4
        if: github.event_name == 'pull_request' || github.event_name == 'merge_group'
        with: { name: tested-tree, path: tested-tree/tree }
      - name: Reuse a green run
        id: reuse
        if: github.event_name == 'push' || github.event_name == 'merge_group'
        env:
          GH_TOKEN: ${{ github.token }}
          EVENT: ${{ github.event_name }}
          HEAD_REF: ${{ github.event.merge_group.head_ref }}
          GROUP_BASE: ${{ github.event.merge_group.base_sha }}
        run: |
          PARENT=$(git rev-parse --verify --quiet HEAD^1 || true)
          aphrollo ci reuse -repo "$GITHUB_REPOSITORY" -sha "$GITHUB_SHA" \
            -tree "$(git rev-parse HEAD^{tree})" -workflow .github/workflows/ci.yml \
            -event "$EVENT" -head-ref "$HEAD_REF" -base-sha "$GROUP_BASE" -parent "$PARENT" \
            -require 'test=Run the tests' >> "$GITHUB_OUTPUT" || echo reuse=false >> "$GITHUB_OUTPUT"
  test:
    needs: changes
    if: needs.changes.outputs.reuse != 'true'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Run the tests
        run: go test ./...
```

`-require job=step-prefix` names a job and the step of it that must have run to
success in the earlier run; repeat it for every job that stands down. The
answer is `reuse=true` only for a first-attempt, green run from this repo whose
recorded tree equals this commit's; on a merge group, only when the group holds
the one pull request (its head commit's first parent is the group's base). Any
doubt, a failed lookup included, prints `reuse=false` and the full suite runs.
The reason is on stderr. A required check whose job is skipped counts as passing;
a matrix job skipped at job level does not report its expanded names, so keep
such a job running on `merge_group` and skip its steps instead.
`aphrollo check` warns when a workflow listens on `merge_group` and never calls
`aphrollo ci reuse`.

## The gate in one screen

- **Edit:** after every Edit/Write the hook runs the related tests and prints
  one `gate:` line (`green`, `red-missing-impl`, `red`, `TIMEOUT`, …), which
  also names every ratchet refusal the commit would raise, with its escape.
- **Turn end:** `Stop` and `SubagentStop` block the end of a turn once when a
  deferred run finished red after Claude's last hook, with that run's gate line
  as the reason (never twice in a row; a red already shown may end the turn);
  `TaskCompleted` exits 2 with the failing tests while the task's tests are
  red. `/gate off` switches all three off.
- **Session switch:** the `/aphrollo` command with `off`, `on` or `status` (`/tdd` is the same switch)
  silences every session hook for the session: edit-time guidance and denies,
  test runs, gate lines, turn-end checks, injected context and the reply style,
  and the statusline shows `off`. The git-side gates (commit, merge, push)
  stay on. Each flip is an event (`override-off` / `override-on`
  with `switch=session-off|session-on`), counted as a wrong-block signal.
  `aphrollo install` writes the `aphrollo` skill the command needs.
- **Commit:** secret scan → staged-baseline guard → ratchet laws → docs → vet/lint →
  fail-first (the staged test must be RED without the change). Suites are
  `NOT RUN` here and run at the merge.
- **Secret scan (commit):** `gitleaks` runs over the staged diff of every
  non-merge commit, before any suite, with the repo's `.gitleaks.toml` when it
  has one. Install gitleaks (8.19 or later uses `git --staged`; older uses
  `protect --staged`): without it, or when it fails or outlasts 30 s, the stage
  prints one `NOT RUN` line and the commit goes on, never a silent pass. A
  finding refuses the commit for an agent and a person alike, with file:line,
  rule, the finding's fingerprint and the local gitleaks version (a line says
  when CI pins another). Clear a false positive in one of three ways: put
  `gitleaks:allow` in a comment on that line, in the file's own comment syntax;
  add the fingerprint to `.gitleaksignore`; or add an `[allowlist]` to
  `.gitleaks.toml`. The merge does not scan: CI's `scan` job covers the PR range.
- **PR:** `workspace pr`/`submit`/`ship` measure the lane's mutants first.
- **Merge:** `workspace merge` runs the suites and the mutation measurement on
  the merged tree, refuses an unaccepted survivor or timeout, and a
  not-covered or inconclusive mutant on a line the lane adds, then prints a
  retro when the PR's journey had friction. On a base branch with a merge queue
  (a `merge_queue` rule in the branch's rules) GitHub takes no direct merge, so
  it enqueues the PR bound to the judged head instead: the lane, text, the PR's
  own checks and the merged tree's laws are judged as before, but a CI verdict for
  an older base is not refused, since the queue tests the current merge itself;
  `--wait` waits until GitHub has merged the PR, or reports why the queue removed
  it, and `--wait <pr>...` enqueues every PR before waiting for any; every line it prints begins with a UTC `HH:MM:SS`. The merge is judged in one checkout kept per repo (`gate-prmerge-warm` beside the lanes; local CI uses `gate-prmerge-localci`) and reset to the merge tree each time, so caches keyed by directory stay warm; a checkout another merge holds is not waited for, a fresh one is built, and `gate gc` removes a warm one only after it sits idle past the gc age. A merge made outside it (the GitHub
  web UI, `gh pr merge`, a terminal) is recorded once per commit as a `merge`
  event by=outside plus an `escape` event of verdict outside-merge, when local
  trunk takes it in: from `workspace sync`, or the post-merge hook in a repo
  with an `aphrollo.toml`, a `.ratchet` directory or a Cargo
  `[workspace.metadata.aphrollo]` table. `workspace sync --since <ref>`
  backfills a range once (`--dry` names the merges and writes nothing).
- **Long suites:** a `go test` package list the recorded per-package times say
  would not fit one run's 600s budget is cut into runs that each do, instead of
  timing out whole. The merge's `-race` list and the commit gate's plain list
  take the same path. The gate records each package's own seconds from every
  `go test` run it makes (the newest twenty, p90, weighed ×1.5; 90s for a
  package never recorded, 30s without `-race`), and a list that fits stays the
  one command it was. Every package is in exactly one run, with the flags it
  always had. Runs go side by side up to the build-slot count
  (`box.mech_parallel` in the user's config overrides), each starting through the
  memory-headroom wait and under the memory cap, all inside one overall cap
  (`budgets.mech_total_s`, 2700 by default). The verdict is green only if
  every run is; a run that does not finish makes the result a timeout that
  names its packages and the runs that never started, never a pass, and a run
  that fails ends the starting of new ones.
- **Race runs:** a Go merge runs `-race` over the packages the change touched,
  and the packages that import them in a second run without it, both with
  `-count=1 -shuffle=on`; the merge is green only if both are, and a refusal
  names both runs (`race-scope = "all"` keeps one `-race` run over everything).
  Race runs on one box go side by side as far as the box carries them: the
  smaller of free memory / 8 GB and cores / 8, never more than
  `box.build_slots`, the pool cargo builds share, and one when free memory
  cannot be read. A run that has to wait says where it stands on one line, at
  most once a minute: `gate: merge queue position 2 of 4 (est. ~11 min; holder:
  <lane> pid <n>)`, the estimate taken from the recent `go test -race` runs in
  the event log and left out when there are none.
- **Walls:** the primary checkout is merge-only; discarding commands are
  refused (`gate allow <wall>` arms one command).
- **Memory:** every test, suite, lint and mutation process the gate starts runs
  under a hard memory cap, so a runaway kills only itself. On Linux with a user
  systemd manager the child runs in a transient scope (`MemoryMax`, no swap) and
  the kernel enforces it; without one, a watchdog sums the process group's
  resident memory and ends it at the cap; on Windows a job object's job memory
  limit refuses the allocation. The default cap is 75% of RAM, or the memory
  available now if smaller, split across the build slots (a mutation run gets
  the whole pool, and only its runaway worker is ended); `memory-cap` overrides
  it. A run the cap ends is reported `OOM-KILLED at <cap>` and is inconclusive,
  never red and never a timeout. A suite or measurement waits for headroom
  (an eighth of RAM, 2-8 GB, double while swap is 90% full) and is refused with
  the numbers when it never comes. Gate scratch (`GOTMPDIR`, cargo's temp) is on
  disk beside the worktrees, not in `/tmp`; the session-start sweep (every repo
  the gate worked in lately) and a pass after each merge and mutation run
  reclaim the scratch, mutation areas and stub dirs of runs that are over, and
  leave everything a live run holds.

Mutation runner contract: [docs/mutation-runner.md](docs/mutation-runner.md).
Law schema, matcher kinds and baselines: [.ratchet/README.md](.ratchet/README.md)
(generated from `internal/tdd/ratchet_laws.md`).

## Configuration

`[aphrollo]` in `aphrollo.toml`, or `[workspace.metadata.aphrollo]` in a cargo
workspace's `Cargo.toml`. `aphrollo config` prints the opt-in keys (the first
rows) with this repo's values; a repo's first `aphrollo install` prints them once. `aphrollo config show` prints every setting of the schema (`internal/config`) with the layer it came from (built-in, the user's `config.toml`, the repo's `trellis.toml`, or the `aphrollo.toml` key it is an alias of), and names a key or value a file got wrong; `aphrollo config set <key> <value> [--user or --repo]|--repo] [--dry]` writes one.

The box's own settings (`budgets.edit_s`, `lock_wait_s`, `cargo_wait_s`, `git_wait_s`, `lint_wait_s`, `deferred_max_s`, `mech_total_s`, `box.build_slots`, `box.mech_parallel`, `reply_style`) live in the user's `config.toml` (`aphrollo config show` lists them). They were environment variables; the old `APHROLLO_*` names still work for one more release and print a notice naming the key. `TRELLIS_OFF=1` switches the gate's session checks off for a process, as `/aphrollo off` does for a session; `TRELLIS_CONFIG` and `TRELLIS_DATA` move the config and data roots. Every other `APHROLLO_*` variable is a test seam or a marker the gate sets for its own children, registered in `.ratchet/dev_instrument_registry.txt`.

With `undercover = true` a tool identity is refused at commit, pre-push and `workspace merge` and flagged at session start and by `gate doctor`; the Bash/PowerShell hook, the git shim, pre-push and `workspace create`/`claim`/`pr`/`ship`/`submit` refuse a tell ref name; `pr`/`ship`/`submit`, `issue`, `feedback` and the Bash/PowerShell hook (for a `gh pr`, `gh issue` or `gh api` call) check text before `gh`. The Bash/PowerShell hook judges `gh pr`/`gh issue` create, edit, comment, review and merge text, and a `gh api` request's title, body and head fields, `--input` JSON file and GraphQL mutation. Every check runs on this box before the text reaches GitHub; nothing in CI repeats it.

One ref name is let through: in a cloud session (`CLAUDE_CODE_REMOTE=true`) the branch the platform assigned, named by `APHROLLO_ASSIGNED_BRANCH=<branch>`, matched byte for byte, never by pattern. Every branch a session names itself stays refused. `workspace merge` always writes its own merge subject (the PR title and number), so GitHub's "Merge pull request #N from <branch>" line never reaches history.

| key | effect |
|---|---|
| `mutants-at-merge` | off by default: mutation measurement of the merged tree before every merge; cost: high CPU and wall-clock: a lane runs tens of mutants, each re-running its package's suite |
| `mutants-before-pr` | off by default: the same measurement before `workspace pr`/`ship`/`submit` open a PR; cost: the mutants-at-merge cost, paid before the PR opens |
| `mutants-at-commit` | off by default: mutation of the lines a commit adds, run against the tests selected for each mutant's function, before the commit lands; a survivor is reported and never refuses it unless the repo pins "block"; cost: up to the budget of wall-clock per commit on bounded workers, under the memory cap; a box with no headroom or a busy mutation lock measures nothing and CI decides |
| `mutants-commit-budget` | 90 by default: the seconds the commit-time run may spend; mutants it does not reach are reported NOT MEASURED, never refused; cost: a higher figure holds a commit up longer on a slow box |
| `mutants-at-merge-level` | report by default: what CI's mutants-verdict does with a survivor: report it and pass, or (block) fail the check and so the merge; cost: block holds a merge on every unaccepted survivor, timeout or unjudged line the PR adds |
| `mutants-integration-packages` | none by default: package directories whose mutants stay settled against the tests of the packages that import them; every other package's mutant its own tests miss is refused at once; a package whose tests take longer to run than one build of them runs only the tests that execute a mutant's line, importers' included; cost: each listed package's missed mutants run the importers' suites, nearest first, within a total time cap |
| `mutants-test-tags` | none by default: build tags the repo's tests need (an integration tier), passed to the commit-time run's coverage build and every mutant run, and the settle run's per-test coverage runs the tagged tests that execute a mutant's line after the unit tests, only for a survivor, one test at a time; a tagged suite that cannot run leaves its mutants NOT MEASURED, never survivors; cost: the tagged suites run for every mutant they cover, so a slow suite spends the commit budget sooner |
| `mutants-skip` | crypto/rand.Read by default: calls, as <import path>.<Func>, whose error test no test can drive: the mutants of the err != nil (or == nil) test on the error such a call returns are not run at commit and CI reports them skipped, counted and never judged; your entries are added to the default; cost: an entry for a call that can fail hides the survivors of its error test |
| `mutants-shards` | derived by default: the most shards one measurement splits into; only ever lowers the box's own count; cost: fewer shards: less CPU at once, longer wall-clock |
| `mutants-slots` (box) | 1 by default: measurements this box runs at once, the rest queue (fixed at 1 for now); cost: each slot runs a full shard set, so size it to cores and RAM |
| `memory-cap` | derived by default: the most memory, in GB, one test, suite or mutation run the gate starts may hold before it is killed and reported OOM-KILLED (inconclusive, never red); derived from RAM, free memory and the slot count; off disables it; cost: a cap below what a build honestly needs kills honest work |
| `memory-headroom` | derived by default: the available memory, in GB, a suite or measurement needs before it starts; below it the start waits, then is refused with the numbers; doubled while swap is 90% full; cost: a higher figure defers work on a busy box |
| `race-scope` | changed by default: what -race covers in a Go merge: the packages the change touched, with the packages that import them run without it as a second run; all runs -race over every package in one run, as CI does; cost: all pays -race's several-fold build price on every importer of a touched package |
| `test-cache` | off by default: go's own test-result cache serves the packages a change left alone, in the post-edit suite ("edit") and also the commit's suite ("commit"), instead of every gate run carrying -count=1; the merge and CI always run the whole tree with no cache, and a line reads "N passed (M packages cached)"; cost: a cached pass is the earlier result of an unchanged package: right only for tests whose inputs go tracks (its sources, the env variables and files they read), so list in test-cache-impure the packages whose tests depend on what go cannot see (the network, the clock, a language server, the machine's processes and ports, files outside their temp dirs); at "commit" the commit's suite also drops -shuffle=on, which go never serves from its cache |
| `test-cache-impure` | none by default: package patterns such as ["./internal/git/..."] whose tests go's cache cannot vouch for: with test-cache on, they run apart with -count=1 every time; cost: each listed package reruns at every post-edit run that includes it; a whole-module run (./...) cannot be split and runs uncached |
| `test-select` | off by default: the post-edit Go suite runs only the tests the coverage store says cover the edited functions, plus every test in a test file the edit changed and every test the store does not know, where an edit used to rerun the whole package; the line reads "N passed, selected M of K tests: covering F" or "full suite: <reason>", and the event records selected and total; the precommit fail-first, the merge, CI and mutation always run everything; cost: the store is built by the commit-time mutation flow, never at the edit, so a repo or package without a fresh one, an edit to a var, const, type, init or test helper, or a test file edit, runs the package whole; a selected green is a green of those tests only, and a test that reaches the edited function through a file, the clock or another package can be missed until the commit and merge gates run everything |
| `gocache-cap` | 20GB by default: the size `aphrollo gate gc` trims the Go build cache (`go env GOCACHE`) down to, taking the files unused longest first, only go's own entries, and none used within the last two hours; cost: a smaller cap rebuilds more of what the next build needs |
| `gocache-age` | 12h by default: how long a Go build cache file must have gone unused before the trim may remove it, however far over the cap the cache is; at least 2h, because go refreshes a cache file's time only hourly (a shorter value is refused); cost: a shorter age lets the trim reach into files a recent build used |
| `go-trimpath` | on by default: the gate's go and golangci-lint runs build with -trimpath, so every lane worktree shares one set of Go build-cache entries instead of caching its own copy of every package; "false" keeps absolute source paths; cost: a test that reads its own repository through runtime.Caller sees a module path and must find the repo by its working directory, or the repo opts out |
| `retro-prompt` | off by default: the post-merge retro: after a landed merge with friction, the session's next hook prints the facts and one question per class; cost: text in the session's context after a merge, and the gh calls that collect it |
| `issue-prompt` | off by default: the session-start line with the open issue and escape counts, and the open-escape count in the weekly digest; cost: text in every session's context and one cached gh call per hour; the records and `gate stats` work without it |
| `undercover` | off by default: the commit-msg gate refuses AI attribution trailers; cost: none |
| `ci` | `auto` (default), `local` or `github`: which CI judges `workspace merge`. `auto` uses GitHub's checks and falls back to local CI (`ci run`) when its jobs never start (a billing lock); `local` never waits on GitHub; `github` refuses an outage. `workspace merge --ci <mode>` beats it for one merge; every merge prints which CI judged it and why |
| `ci-jobs` | 1 by default: the jobs local CI (`ci run`, `workspace merge --ci local`) runs at once; `--ci-jobs N` on `ci run` beats it for one run; cost: each extra job is another full build on the box at once, so keep it at 1 on a shared host |
| `ci-reuse` | `true` by default: `workspace merge` on GitHub's green takes CI's `test` (Linux) and `test-windows` verdicts instead of re-running the suites locally, when the merged tree equals the tree CI tested; `false` always runs the local gate; cost: a verdict is trusted for the tree it was measured on, never for a newer one: a green verdict for an older base refuses the merge (exit 2) and asks for a rebase rather than running the local suites |
| `ci-reuse-checks` / `ci-reuse-workflow` | `["test", "test-windows"]` and `"pipeline.yml"` by default: the CI jobs (first required, the rest when present) and workflow file whose green the merge gate takes in place of local suites; only first attempts of `github-actions` checks count; cost: none, a repo with other job names must list them or the gate runs locally |
| `ci-timeout` | `"30m"` by default: the longest one step of a local CI run may take, as a duration such as `"45m"`; `--ci-timeout` on `ci run` beats it for one run; a step that reaches it is stopped and named; cost: a longer limit holds a hung step longer |
| `commit-message-deny` | commit-msg deny patterns |
| `undercover-extra` | extra tokens for the undercover checks, e.g. `["codename"]` |
| `always-run`, `clippy-clean` | suites run on every merge; crates gated on clippy `-D warnings` |
| `fail-first-env` | env switches for the fail-first run |
| `go-test-reads` | `"<path prefix> -> <package dir>"`: a staged path also selects that suite |
| `mutants-env`, `mutation-accept` | the mutation run's env and its accept-list |
| `retro-on`, `retro-slow-merge-minutes`, `retro-sinks` | post-merge retro triggers and questions |
| `issue-labels`, `upstream` | labels `aphrollo issue` accepts; tracker for `aphrollo feedback` |
| `docs-check`, `baselines`, `prune-lanes-on-merge`, `sdd-dir` | docs on commit, guarded baselines, lane sweep, spec root |
| `[aphrollo.precommit]` (`aphrollo.toml` only) | `"<root>" = [["tsc", "--noEmit"], ["eslint", "src"]]`: argv arrays (no shell) run in order in that non-cargo root, replacing its built-in checks (go vet/lint, the npm typecheck and lint); first failure refuses. A declared command has no HEAD baseline unless it is written `{ argv = [...], baseline = "lines" }`: then a failure is run again on HEAD's tree and refuses only over output lines HEAD's run did not print (paths and trailing whitespace aside), or when HEAD's run cannot be made. A command written `{ argv = [...], inputs = ["src/**"] }` names the globs it reads (slash paths from the root; `./` and `\` are normalised; a glob starting with `/` or `..` is refused). The commit gate records its verdict with a content hash of the files they select, the root's lockfiles (`package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `bun.lockb`, `go.sum`, `Cargo.lock`) and manifests (`package.json`, `go.mod`, `Cargo.toml`); the merge gate skips the command, printing `[reuse]`, when the merged tree hashes the same under a green entry. It runs as before with no `inputs`, a glob that matches no file, a glob that selects a git-ignored file (ignored files under `node_modules` are skipped, the lockfiles standing for them), a red or missing entry, a changed tool, or `baseline = "lines"`. Environment variables and any file not listed in `inputs` are not covered: put tool configs (`tsconfig.json`, the eslint config) in `inputs`. The merge gate also records the verdict of every real run it makes, green or red, under the same key, so the next merge over the same inputs reuses it; a root with no changed file is not judged by the commit gate, and the merge's record covers it. A tool named by a path inside the repo (`./scripts/lint.sh`) is keyed by its content. When the merge gate runs a command that declares `inputs` it prints `[run] <cmd>: no reuse — <reason>` (no recorded verdict for this tree, the recorded verdict was red, inputs changed, lockfile/manifest changed, tool changed, a glob matched no file, a glob selects an ignored file, a glob leaves the root, the tool could not be found, the store could not be read); when the commit gate cannot key such a command it prints `[note] <cmd>: not recorded for reuse — <reason>` |
| `[aphrollo.typecheck]` (`aphrollo.toml` only) | `"<root>" = ["svelte-check", "--tsconfig", "./tsconfig.json"]`: one argv whose first word is an npm bin, run as `node <its entry>` from that npm root's node_modules at commit, merge and `aphrollo check`. Undeclared, a root's typecheck is its package.json `typecheck` script, else its `check` script (each `&&` step run without a shell; a script that needs one is NOT RUN), else svelte-check (after `svelte-kit sync` in a SvelteKit app) when it depends on it, else tsc, those two only with a tsconfig.json. tsc, vue-tsc, svelte-check and eslint are judged by the diagnostics they add over HEAD, any other tool by the output lines it adds over HEAD's run |
| `[aphrollo.lint]` (`aphrollo.toml` only) | `"<root>" = ["eslint", "src"]`: the npm root's lint, run the same way, in place of eslint over the staged files |

## Release replay

`go run ./tools/replay -repo .` runs the newest release's binary and this checkout's read-only over trees that did not change, and fails on a hit only the candidate reports. It works in a scratch directory (`-work`, default a temporary one) holding two clones and the builds made from them, several gigabytes. A replay that passes removes what it made there, and the directory when it made it; a `-work` that already holds one of the names it creates (`area`, `bin`, `previous-src`, `self`, `synthetic`, `self-store`, `synthetic-store`) is refused rather than overwritten, and nothing else in it is touched. A replay that fails keeps the directory and prints its path, because what failed is in it. `-keep` keeps it after a pass too, for inspection. A replay killed before it could clean up leaves a `replay-<digits>` directory in the temp dir, which the next `aphrollo gate gc` removes once no process holds it.

## Known limitations

- LSP columns are UTF-16 code units; `--symbol` resolves regardless.
- Unified diffs assume newline-terminated files.
