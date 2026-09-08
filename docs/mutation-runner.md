# The mutation runner contract

The binary runs the mutation measurement itself. There is no producer script
in the consuming repo, no environment-variable protocol between the two, and
no document signed at the end of a run: the measurement and the judgement are
one event, in the foreground, on the tree that is about to land.

Two entry points, one code path (`internal/tdd/mutants_measure.go`):

- the **pre-merge stage** (`internal/tdd/mutants_stage.go`), which runs when
  the repo declares `mutants-at-merge = true` and refuses the merge on an
  unaccepted survivor;
- **`aphrollo gate mutants run`**, which measures THIS checkout against its
  base and exits 1 on the same finding. Nightly CI on `main` calls it with
  `--base <checkpoint>` (`.github/workflows/nightly-mutants.yml`).

What this replaces refused 150 merges over three weeks, logged no reason for
141 of them, and named a surviving mutant in none: it judged a receipt, and
the seven paperwork checks it ran on that receipt sat ahead of the only
question worth asking. The rest of this page is what the run does instead.

## What a repo declares

One table, the same one ratchet is configured from: `[workspace.metadata.aphrollo]`
in `Cargo.toml` for a Cargo workspace, `[aphrollo]` in `aphrollo.toml` for
everything else. The Cargo spelling wins when a repo has both.

| key | type | meaning |
|---|---|---|
| `mutants-at-merge` | bool | run the measurement at pre-merge-commit. Absent or false: the stage logs `mutants-skipped:not-declared` and passes |
| `mutants-env` | string array | `NAME=VALUE` switches exported for the run — the suites a mutant's code is only reachable from |
| `mutation-baseline-exclude` | string array | `"<nextest filter> # why"` entries, folded into one `-E not(...)` for the run's whole test invocation |
| `mutation-accept` | string array | the survivors somebody signed off on, with a reason each |
| `mutants-after` | string | a repo-relative path run after judgement, in the worktree. A path with no file there is a refusal, not a silent skip |

Four keys are **retired** and refused by name, before any suite runs, with
`mutants-at-merge` named as the replacement: `mutation-receipt`,
`mutants-local`, `mutants-judge-local`, `mutation-runner`. The trio encoded
where a receipt was produced and where it was judged; the fourth named the
script that produced it. A repo still declaring one believes it is gated and
is not, which is the whole reason the refusal is loud rather than a shrug.

```toml
[workspace.metadata.aphrollo]
mutants-at-merge = true
mutants-env = ["FORGE_GPU_TESTS=1"]
mutation-baseline-exclude = [
  "test(conditioner_burst) # box-contended wall-clock test; fails under load whether or not a mutation is applied (issue #265)",
]
mutants-after = "tools/mutants_after.sh"
```

Every switch named in `mutants-env` must also be registered in the repo's
dev-instrument registry — an env switch that gates a suite is exactly the kind
that law exists to catch.

## What is measured

The measured side is always the **working tree** against a base:

- in a lane checkout, the base is `merge-base(HEAD, <default branch>)` — the
  newest trunk commit the lane already contains, so a catch-up merge does not
  charge the lane for everything trunk did in between;
- at **pre-merge-commit**, HEAD is still trunk and the merge result exists
  only in the index and the working tree, so the base is
  `merge-base(HEAD, <incoming tip>)` and the merged tree is what gets
  measured. The tip is read from `MERGE_HEAD`, and from `GIT_REFLOG_ACTION`
  when there is none — a clean automerge fires this hook BEFORE `MERGE_HEAD`
  is written, and the reflog action is then the only signal that names the
  branch coming in. Neither resolving is a refusal that says so;
- `--base <sha>` overrides both.

The pre-merge stage measures a lane LANDING ON TRUNK and nothing else. Two
other things reach the same hook and are stood down with a logged reason
rather than measured or refused:

- a **catch-up merge** of trunk into a lane (HEAD is not the default branch),
  which is the push guard's own printed remedy. Nothing lands: the base would
  become the fork point, so the lane would pay for every change trunk made
  since, and a survivor trunk already accepted would refuse the catch-up.
  Token `mutants-skipped:catch-up`.
- a **conflicted cherry-pick or revert**, concluded with `git commit`, which
  git routes through `pre-commit` and the gate routes into the merge routine.
  Neither writes `MERGE_HEAD` nor a `merge <ref>` reflog action, and nothing
  is being merged. Token `mutants-skipped:not-a-merge`.

File selection is `git diff --name-only <base> -- 'crates/**/src/**.rs'` and
the diff handed to cargo-mutants is `git diff <base> -- <those files>`, with
no `HEAD` operand in either: selection and content are one diff, or a run
selects nothing while the diff file still describes the worktree. A diff
naming no mutable source passes with `mutants: nothing to measure (0 changed
source files)` and runs the tool zero times.

### The tree is checked, not trusted

cargo-mutants mutates the tree IN PLACE — that is what makes the run cheap
(no 135 MB copy per mutant, no cold rebuild of the world) and it is the one
way the run can damage what it is judging: the tool restores each mutation as
it finishes with it, so a run that is killed, or that dies on a full drive,
leaves the last mutation in the source. Merging that merges a mutant.

So the whole `git diff` against HEAD is snapshotted before the run and
compared after it, on every exit path including a no-verdict exit and the lone
re-run. The comparison is with ITSELF, never against a clean tree: at
pre-merge-commit the worktree legitimately carries the whole merge result, so
"is it clean" is the wrong question and would refuse every merge. A difference
refuses, with `git diff --stat` and the exact `git checkout -- <files>` that
restores them. The tree is **never** restored automatically — a tool that
rewrites source on its way out is not one anybody can reason about
mid-incident. Token: `mutants-refused:tree-changed`.

## The argv

Exactly this, for a Cargo repo:

```
cargo mutants --copy-target=true --in-diff <diff> --no-shuffle --test-tool=nextest \
  --minimum-test-timeout <T> --timeout-multiplier 3 \
  [--package <p> ...] [-- -E not(<filter>)]
```

- **no `--in-place`; `--jobs N` from the box.** cargo-mutants copies the
  tree once per job under the run's temp dir (`.mutants/<worktree name>` beside
  the worktree, on its disk, never the OS temp dir) and mutates the copies side by side; the
  checkout itself is never touched. In place was one tree and therefore one
  job (the two flags refuse each other), which measured 739 mutants in 16 h
  on a box that could run eight copies. N is `MutantsJobsCap`: cores/3 and
  RAM/8 GB, at most 8; each copy builds in its own target dir, and the disk
  budget refuses a run that cannot afford N copies. `--copy-target=true`
  copies the lane's warm target dir into every copy, so no copy builds cold;
  one target dir shared by all copies is not possible, cargo serialises
  builds on its build-directory lock.
- `--no-shuffle` — two runs of the same tree must name their mutants in the
  same order, or one report cannot be compared with the one before it.
- `--package` — one per crate the diff touches. This scopes the unmutated
  BASELINE as well as the mutant pool: a wall-clock test failing in some other
  lane's untouched crate must not veto this measurement (issue #251). Absent
  when the touched-crate set could not be determined, which means the whole
  workspace.
- `-- -E not(<filter>)` — present only when `mutation-baseline-exclude` is
  non-empty. Everything after `--` is cargo-mutants' own passthrough to the
  test tool, forwarded to the SAME `cargo nextest run` it invokes for the
  baseline and for every mutant, so one flag covers both.
- `--run-ignored`, `--ignored` and `--include-ignored` never appear, in any
  position, whatever the repo declares. nextest's filterset language has no
  `ignored()` predicate, so "these ignored tests and no others" cannot be
  expressed on a command line at all; the measured set is the nextest
  profile's own `default-filter` intersected with
  `not(<mutation-baseline-exclude>)`, and nothing else.

A timed-out mutant is re-run once with an anchored `--re` naming only the
mutants under re-examination, and nothing else added — it is alone on the box
because an in-place run is one job to begin with. Nine timeouts on one lane
were all contention, and a refusal that names contention as a survivor is a
false report; one that times out again with the box to itself stays
**unmeasured**, which is not the same as caught, and refuses the merge by
name.

### Timeouts

`<T> = max(3 × last baseline seconds, 120)`. The last baseline is read from
`<state>/mutants/<repo>/baseline_seconds` (120 s when absent) and written back
from the run's own `Unmutated baseline in Ns build + Ms test` line — a budget
derived from a suite that actually ran on THIS box is the only one that tells
a slow test from a mutant that hangs. Nine timeouts were measured at
cargo-mutants' own 30 s default while eight cold builds shared the box.

### Concurrency

A **Cargo** run is one job, always, and says so rather than deriving a number
nobody can use:

```
mutants: in-place, 1 job (cargo-mutants forbids --jobs with --in-place)
```

There is nothing to override — no `--jobs` on the argv and none on
`gate mutants run`, which refuses one with `flag provided but not defined:
-jobs` rather than accepting a number the tool will reject (issue #592). The
free-space budget below is therefore one job's worth.

A **Go** run (gremlins) copies nothing into the tree it measures and takes a
worker count happily, so it keeps the per-box cap
`min(cores / 6, ram_gb / 6, 2)`, floored at 1, and PRINTS it with the limit
that bound it — "2 jobs" without "cap 2" says nothing:

```
mutants: 2 jobs (min(cores 24/6=4, ram 64GB/6=10, cap 2) — cap 2)
```

Memory that cannot be READ is not memory that is absent: an unreadable reading
prints `ram unknown` and lets the cores decide alone. `--jobs` is gone from
both halves: the Go run derives its count from the box it is on, and no
caller may type a number for either.

**One run per BOX, not one per repo.** The call is wrapped in a machine-wide
advisory lock held for its whole duration, cold build included. A wall-clock
test with `threads-required = "num-cpus"` has already declared that one run
needs every thread on the box, and four runs sharing it 4:1 measure nothing
usefully for any of them (issue #253). A second run queues, announcing who it
is waiting for about once a minute. There is no timeout on that wait.

### Temp dirs, build dir, free space

Three runs died at mutant 101 of 131 on a full disk: `TMPDIR` alone was
exported, a Windows cargo-mutants ignored it, and the tree copies took `C:`
from 40 GB free to 12 GB. So:

1. **All three** of `TMPDIR`, `TMP` and `TEMP` are set, on every platform, to
   `<resolved target dir>/mutants`. Setting one and inheriting the others is
   the bug: whichever name the tool reads is the one that decides.
2. `CARGO_TARGET_DIR` is `<resolved target dir>/mutants/target` — the run's
   own build directory, which nothing the build queue schedules ever compiles
   into. That is what makes skipping the queue safe rather than merely faster:
   a mutation build owns its directory for hours behind cargo's own blocking
   lock.
3. Free space on that drive is checked against `jobs × 15 GB` BEFORE the run
   starts; a shortfall refuses with one line naming both numbers and logs
   `mutants-refused:disk`. A drive whose free space cannot be read never
   refuses — this side's own blind spot must not stop a run that would have
   been fine.

`APHROLLO_MUTATION_GATE=1` marks the run's children. That one marker answers
both questions the cargo shim asks: a bare `cargo mutants` is refused and told
to type the verb instead, and a marked run builds without queueing behind
every editor on the box. `gate doctor` reports free space on the drive holding
each project's target dir and warns under 30 GB.

### Env-gated suites and the nextest profile

Mutants in code only a gated suite reaches are missed by definition: 101 of
167 mutants on one lane lived in render-world code reached only by GPU parity
tests. Each `mutants-env` entry is exported for the run, and
`NEXTEST_PROFILE=mutants` is set when the repo's `.config/nextest.toml`
declares `[profile.mutants]` (asked at the repo root and at the cargo
workspace root, since a workspace keeps it at the latter).

That profile is also where a repo selects its measured set. A tier that needs
an environment stops being `#[ignore]`d, is made addressable by the filterset
language (its own test binary, a package, or a name pattern), is excluded by
`default-filter` in the everyday profile and included in `[profile.mutants]`.
`#[ignore]` carries at least three unrelated meanings in a real consuming
repo, so selecting by that attribute selects a category that does not exist.

### The baseline and the mutants share one profile — cargo-mutants gives no way to split them

A repo cannot give the unmutated baseline `retries > 0` while keeping
`retries = 0` for every mutant. Checked directly against cargo-mutants
27.1.0's own source and config schema, not inferred:

- `NEXTEST_PROFILE` (the env var `--test-tool=nextest` runs under, and how
  `[profile.mutants]` gets selected at all — confirmed against
  `cargo-nextest`'s own CLI, `dispatch/common.rs`: `--profile` is declared
  `env = "NEXTEST_PROFILE"`) is set ONCE, before `cargo mutants` starts, and
  cargo-mutants never touches it again: it is inherited unchanged into every
  child process the run spawns.
- Inside cargo-mutants, `cargo_argv`/`run_cargo` (`src/cargo.rs`) build the
  test-tool invocation from `Phase` (`Build`/`Check`/`Test`) and the global
  `Options` alone. Neither function takes the `Scenario` (`Baseline` vs.
  `Mutant(_)`) that `src/lab.rs`'s `run_baseline` and per-mutant runs both
  eventually call through — so the unmutated baseline and every mutant
  literally run the identical argv and environment for the test phase.
- `cargo mutants --emit-schema config` — the tool's own config schema — has
  no baseline-specific key: `additional_cargo_test_args`, `test_tool`,
  `timeout_multiplier` all apply uniformly to `Phase::Test` regardless of
  scenario. There is no `--baseline-test-arg` or equivalent CLI flag either.

So the two questions ("does this test catch the mutation?" and "is the tree
green before we start?") share one process-wide test invocation because
cargo-mutants' architecture shares it. Two remedies work without changing
anything here:

- **`mutation-baseline-exclude`** solves it for a NAMED test: excluded from
  both phases, it can never veto a measurement under load again. The cost is
  permanent — a mutant caught only by an excluded test is never caught again.
- **Make the test load-insensitive** (an ephemeral port,
  `threads-required = "num-cpus"`, a dedicated `test-group`) so it stops
  competing with the run's own concurrent build. This is the only remedy that
  keeps full mutation coverage, and it is work in the consuming repo on the
  named test, never a runner-side setting.

## Reading the answer

Outcomes are read from `mutants.out/outcomes.json` as cargo-mutants writes it,
never from its log text. Any outcomes file an EARLIER run left behind is
deleted first: it describes a different tree, and a run that writes none
reached no verdict — the difference between those two disappears if the stale
file is still sitting there.

Exit status 0, 2 and 3 are the ones cargo-mutants uses to describe a completed
run. Anything else means the run stopped, and a stopped run's silence must
never read as "nothing survived": it refuses with the status and the path to
`mutants.out/log`, logging `mutants-refused:no-verdict`.

## The judgement

A `missed` mutant whose name is not in `mutation-accept` refuses. The report
leads with the finding, because that is what somebody has to act on:

```
crates/editor_client/src/creator.rs:101:33: replace || with && in send_undo_redo
mutants: 12 tested, 10 caught, 1 unviable, 1 missed (0 accepted), 0 unmeasured
mutants: write the test that fails, or add the line to mutation-accept with a reason (…)
```

`, K not covered` is appended to the counts only when a gremlins run reported
not-covered mutants; they are counted on their own and never as unviable.

An accept entry comes in one of three key shapes, each carrying a reason.
`"<file>:<line> <mutation> # why"` matches that mutation on that line, at
whatever column it is at. `"<file>:<line>:<col> <mutation> # why"` names one
mutant among several sharing a line, the only way to accept one of them
without admitting its unexamined siblings. `"<file> <mutation> # why"` drops
the line altogether and matches that mutation anywhere in that file, at any
line and any column, for a list that is reviewed once and must not rot the
next time an edit above the mutant shifts it (issue #578) — it is applied to
every site it finds, and the report says how many:

```
mutation-accept: line-free entry for <file> <mutation> admits N sites
```

The most specific shape that names a survivor wins: column, then line, then
line-free. A column-less entry that cannot tell its own line's mutants apart
is refused rather than guessed at, unless a line-free entry behind it admits
them all anyway. A location written line-keyed whose line did not parse
(`lib.rs:4x`, `lib.rs:12:x`, or a path with a space in it) is refused and
quoted back, never re-read as a line-free key that could match nothing.

The reason may open with a machine-readable `kind=` directive naming which
claim it makes:

```
kind=equivalent: <why the two forms compute the same thing>
kind=unobservable-runner test=<TestName>: <why only that test covers it>
kind=unobservable-capability issue=<ref>: <why no test can exist yet>
```

A reason with no directive is legacy shorthand for `kind=equivalent`; a
misspelled kind is refused by name rather than read as an equivalence claim.
An entry with no reason at all is not an accepted survivor: a list nobody had
to justify is a list of survivors somebody silenced.

`mutants-after` runs last, in the worktree, with the verdict in
`APHROLLO_MUTANTS_STATUS` ("0" passed, "1" refused). Its own failure is logged
and never changes the verdict — a cleanup script that fails must not turn a
clean measurement into a refused merge, nor a refused one into a pass. The
binary must not know what a test's side effects are; a repo whose tier writes
to a shared database owns reclaiming the rows a timeout-killed test binary
left behind, and this is the one hook it gets.

## How a mutant is named

cargo-mutants 27.1.0 names a mutant `<file>:<line>:<col>: <mutation>`:

```
crates/editor_client/src/creator.rs:101:33: replace || with && in send_undo_redo
crates/editor_client/src/creator.rs:101:16: replace || with && in send_undo_redo
```

Those are two DIFFERENT mutants. The column is part of the identity, not
decoration: that file emits several distinct mutants on line 101 with
identical text (issue #282). So an accept-list entry and a re-run's own name
filter both carry the column and use the tool's own line VERBATIM, anchored:

```
--re '^crates/editor_client/src/creator\.rs:101:33: replace \|\| with && in send_undo_redo$'
```

A rebuilt name that drops the column matches nothing, so the filter selects
nothing and the run measures the whole diff again. A column-less accept entry
matches by file, line and mutator alone — fine for the common case of one
mutant per line — but it is refused when the line turns out to carry more than
one mutant, so an unexamined sibling stays unaccepted and blocks rather than
being admitted alongside the one somebody reviewed — unless the list also
carries a line-free entry for that mutation, which admits every site of it by
definition and so has already spoken for both.

## Go repos

A repo whose worktree has a `go.mod` and no `Cargo.toml` is measured with
**gremlins** (`internal/tdd/mutants_go.go`), scoped to the same base diff and
judged by the same code. The choice was measured rather than argued:

| tool | on this box |
|---|---|
| gremlins v0.6.0 | installs and runs; `--diff <ref>` scopes to the lane, `--output` writes machine-readable results, `--workers` caps concurrency. Analysis of `internal/tdd`: 1626 runnable mutants, 270 not covered, 85.76% mutator coverage, 2.6 s behind one full coverage run |
| go-mutesting | does not BUILD on windows/amd64 — its `zimmski/osutil` dependency uses `syscall.Dup` and `syscall.RLIMIT_NOFILE`, neither of which exists there. No wall time to compare |

gremlins' statuses map onto the same vocabulary: `KILLED` → caught, `LIVED` →
missed, `NOT COVERED` → not covered (never a survivor on its own — no test
ran, so nothing "did not notice"), `TIMED OUT` → timeout, anything else →
unviable. An unrecognised status is never read as caught.

**On Windows the stage stands down.** `gremlins unleash --dry-run` reports
`Runnable: 0, Not covered: 4890, Mutator coverage: 0.00%` on this repo's own
tree: Go's coverage profile comes back empty and every mutant is filed NOT
COVERED. A dogfood lane measured 50 mutants / 0 caught / 50 survivors on
Windows against 50 / 44 caught for the same tree on Linux. Reporting that
would be wrong in the direction that blocks merges, so the stage logs
`mutants-skipped:gremlins-windows` and passes, and Linux measures instead —
nightly CI runs `gate mutants run --base <checkpoint>` against `main` and
records an escape on a non-zero exit. On a box whose gremlins reports non-zero
mutator coverage the stage measures with no further configuration.

Two standing gaps, named where they were found rather than in an issue nobody
reads: gremlins gathers coverage from the UNIT suite only, so a repo whose
real logic sits behind `//go:build integration` tests gets an empty or
misleadingly-clean report over that code; and it runs the WHOLE module's suite
once before it mutates anything, so a single flaky test anywhere aborts the
run with `failed to gather coverage`, naming coverage rather than the test
that caused it.

## Gate-log tokens

Every verdict writes one line, so `gate stats` can answer how many merges the
stage refused and for what without re-running anything.

| token | meaning |
|---|---|
| `mutants-passed:tested=…,caught=…,unviable=…,missed=…,accepted=…,unmeasured=…,notcovered=…` | a run that reached a verdict and found nothing unaccepted |
| `mutants-refused:` + the same counts | a run that reached a verdict and found a survivor or an unmeasured mutant |
| `mutants-refused:disk` | not enough free space for `jobs × 15 GB` |
| `mutants-refused:tree-changed` | the run left the working tree different from how it found it |
| `mutants-refused:git-failed` | git could not read the tree, so the tree that was measured cannot be compared with the one the run started from — the refusal carries git's own stderr |
| `mutants-refused:no-verdict` | an exit status cargo-mutants does not use for a verdict |
| `mutants-refused:config` | a retired key, or a `mutants-after` naming a file that is not there |
| `mutants-refused:no-lane-tip` | neither `MERGE_HEAD` nor `GIT_REFLOG_ACTION` named the branch coming in |
| `mutants-refused:runner-failed` | the runner never started, so nothing was measured |
| `mutants-skipped:not-declared` | the repo declares no `mutants-at-merge` |
| `mutants-skipped:catch-up` | trunk merged INTO a lane; nothing lands, so nothing is measured |
| `mutants-skipped:not-a-merge` | a conflicted cherry-pick or revert being concluded |
| `mutants-skipped:nothing-to-measure` | the diff named no mutable source |
| `mutants-skipped:gremlins-windows` | the Go runner cannot measure on this platform |

A run that reached a verdict lands in the green/red columns of the `mutants`
row. One that never measured anything is counted by its reason alone: a red
there would read as "a mutant survived" and send a reader looking for one that
was never measured.
