# The mutation runner contract

The binary runs the mutation measurement itself. There is no producer script
in the consuming repo, no environment-variable protocol between the two, and
no document signed at the end of a run: the measurement and the judgement are
one event, in the foreground, on the tree that is about to land.

Two entry points, one code path (`internal/tdd/mutation/mutants_measure.go`):

- the **pre-merge stage** (`internal/tdd/mutation/mutants_stage.go`), which runs when
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
| `mutants-at-merge` | `true`, `false` or `"ci"` | `true`: run the measurement at pre-merge-commit. `"ci"`: the measurement is the PR pipeline's `mutants-verdict` check and the local gate measures nothing (see "Measuring in CI" below). Absent or false: the stage logs `mutants-skipped:not-declared` and passes. Any other value is refused |
| `mutants-before-pr` | `true`, `false` or `"ci"` | `true`: `workspace pr`, `ship` and `submit` measure the lane's diff first. `"ci"`: they skip the run and print `mutants: measured in CI (mutants-verdict)` |
| `mutants-at-merge-level` | `"report"` or `"block"` | `"block"`: `gate mutants verdict` (CI's `mutants-verdict` check) fails on an unaccepted survivor, a timeout or an unjudged line the diff adds, and under `mutants-at-merge = "ci"` the local merge gate refuses a merge whose check did not pass. Absent or `"report"`: the same findings print under `REPORT ONLY`, the check passes, and the merge gate neither waits for the check nor refuses on it. A measurement that is itself broken (missing shard, unreadable accept-list) fails at either level. Any other value is refused |
| `mutants-at-commit` | `true`, `false`, `"report"` or `"block"` | `true` or `"report"`: the commit gate mutates the lines the commit adds and runs each mutant against the tests selected for its function (see "At commit" below). Go repos only. `"block"`: a survivor also refuses the commit. Absent or false: the stage is inert and prints nothing. Any other value is refused |
| `mutants-commit-budget` | positive integer | the seconds the commit-time run may spend, 90 when absent. A value that is not a positive whole number is refused |
| `mutants-integration-packages` | string array | package directories (for example `"internal/cli"`) whose mutants are settled against the tests of the packages that import them; see "Mutants nothing judged" below. Every package not listed is judged by its own tests alone |
| `mutants-env` | string array | `NAME=VALUE` switches exported for the run — the suites a mutant's code is only reachable from |
| `mutation-baseline-exclude` | string array | `"<nextest filter> # why"` entries, folded into one `-E not(...)` for the run's whole test invocation |
| `mutation-accept` | string array | the survivors somebody signed off on, with a reason each |
| `mutants-after` | string | a repo-relative path run after judgement, in the worktree. A path with no file there is a refusal, not a silent skip |
| `mutants-build-jobs` | integer | how wide ONE shard's cargo may build, honoured verbatim. Absent: derived from the box (cores and free memory divided between the shards). A value that is not a positive whole number is refused, never quietly derived |
| `mutants-shards` | integer | the most shards a measurement may divide itself into. A CAP: it lowers the count the box derived and never raises it above what that box allows, so a repo on a machine other sessions build on can stop the run taking the whole thing. Absent or zero: derive from the box. A value that is not a positive whole number is refused, never quietly derived |

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

`mutants-env` has a sibling the same shape, read from the same two tables with
the same precedence: **`fail-first-env`**, the switches exported for the
[fail-first](../README.md#tdd--law-gates-aphrollo-gate) proof run. The
mutation run is not the only one a gated suite is invisible to — the proof
builds HEAD in a throwaway worktree, and a suite gated behind a switch that
lives only in the author's shell self-skips there. The fail-first stage used
to read that skip as "your tests pass at HEAD" and refuse a correct commit
(issue #656); it now reports `all-tests-skipped` and refuses with the remedy
instead. They stay two keys because the two runs are separate decisions: a
measurement on the CI runner can afford a switch that would make every commit
on the box pay for a GPU.

```toml
[workspace.metadata.aphrollo]
mutants-env    = ["FORGE_GPU_TESTS=1"]
fail-first-env = ["FORGE_GPU_TESTS=1"]
```

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

cargo-mutants mutates a COPY of the source tree, and restores each mutation
as it finishes with it — but a run that is killed, or that dies on a full
drive, can still leave the last mutation in the tree it copied from. Merging
that merges a mutant.

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
cargo mutants --copy-target=false --in-diff <diff> --no-shuffle --test-tool=nextest \
  --minimum-test-timeout <T> --timeout-multiplier 3 \
  [--package <p> ...] \
  --jobs 1 --output <run temp>/shard-<i> [--shard <i>/<N> --sharding round-robin] \
  [-- -E not(<filter>)]
```

- **no `--in-place`; N PROCESSES, one per shard.** In place was one tree and
  therefore one job (the two flags refuse each other), which measured 739
  mutants in 16 h on a box that could measure eight at once. N is
  `MutantsJobsCap` — cores/3 and 8 GB per shard of whatever memory is free
  (available commit, falling back to total RAM when it cannot be read; see
  the environment section below), at most 8 — and it is a SHARD count
  now: cargo-mutants has no build-directory flag, so the concurrency lives
  above it. N processes, each `--jobs 1`, each given `--shard i/N` with
  `--sharding round-robin` (mutant `i` on shard `i % N`, which spreads cost
  better than a contiguous slice). Shard indexes are 0-based with k < N:
  `--shard 3/3` is refused with "shard k must be less than n", and the union
  of shards 0, 1 and 2 of 3 is exactly the unsharded list, nothing dropped and
  nothing measured twice. N is then lowered — in order — to the repo's own
  `mutants-shards` when it declares one, to what the drive fits, and to the
  number of mutants the diff has, so a small lane does not start empty shards
  that each pay a cold baseline build. Every one of those only ever lowers N,
  and the line the run prints names which of them bound the answer:
  `mutants: 2 shards (min(cores 24/3=8, ram 63GB/8=7, cap 8) — ram, capped
  to mutants-shards = 2)`.
- **`--copy-target=false`: the copy is the SOURCE TREE ONLY.** With it true
  every job's copy also carried the workspace target dir. On a repo with 294
  GB of build products and 37 MB of sources that is a 375 GB byte-for-byte
  copy, because NTFS has no reflink: one measured run spent 1 h 47 min on the
  first copy, left 33 GB free so the other six never fit, ran its 7 requested
  jobs one at a time, and exited 1 with no verdict after 54 of 742 mutants.
  The warm build products live outside the copies instead — one PERSISTENT
  target dir per shard, `<run temp>/target-<i>`, warmed once and kept between
  runs. The first run pays N cold builds in parallel; every later one copies
  megabytes. Distinct dirs per shard is the load-bearing part: cargo
  serialises builds on its build-directory lock, so ONE dir shared by N
  processes would run them one after another.

  What persists is the DEPENDENCIES, never the mutated packages. Whatever a
  previous run left of the packages it mutated was built from mutated source,
  and the tree cargo-mutants copies for the next run carries the original
  mtimes, which are OLDER than those artifacts — so cargo rebuilds nothing and
  the baseline links the previous run's mutant. That was measured: test
  binaries an hour newer than the source they were told to test, a build step
  reporting `Finished 'test' profile [optimized + debuginfo] target(s) in
  0.74s`, and a baseline failing on behaviour the current source cannot
  produce. Each shard therefore cleans the packages the run mutates out of its
  build dir before it starts, and removes the dir whole when it cannot — when
  the run names no package, so the whole workspace is mutable, or when the
  clean itself fails. Nothing mutates a dependency, so dependency artifacts
  are always safe to keep, and they are the whole value of the directory.
- **`--output <run temp>/shard-<i>`.** Each shard writes its own
  `mutants.out`, and the verdict is the MERGE of all N: counts summed,
  survivors from every shard named. A shard that reached no verdict is never
  dropped — the run reaches no verdict, and the refusal names WHICH shard and
  what it exited with. A shard whose slice of the pool was empty (five mutants
  over seven shards) is not that: it exits 0 with `mutants.json` as `[]` and
  no outcomes file, and that empty list is what tells the two apart.
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
mutants under re-examination, and nothing else added. It is ONE unsharded
process with the box to itself, reusing shard 0's warm target dir: a `--shard
i/N` inherited from the run would re-run a fraction of the named mutants and
leave the rest timed out for a reason that has nothing to do with them. Nine timeouts on one lane
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

A **Cargo** run derives its shard count from the box and PRINTS it with the
limit that bound it:

```
mutants: 7 shards (min(cores 24/3=8, ram 63GB/8=7, cap 8) — ram)
```

There is nothing on the command line to override — no `--jobs` on the shared
argv, since each shard carries its own `--jobs 1`, and none on `gate mutants
run`, which refuses one with `flag provided but not defined: -jobs` rather
than accepting a number the tool will reject (issue #592). The one override is
the repo's `mutants-shards`, which lowers the count and never raises it. The
free-space budget below is per shard.

A **Go** run (gremlins) copies nothing into the tree it measures and takes a
worker count happily, so it keeps the per-box cap with a gremlins worker's
own memory price: `min(cores/3, memGB/2, 8)`, floored at 1, against a Cargo
shard's 8 GB. A worker is one `go test` of one package built through the
shared GOCACHE; a two-worker run over this repo's largest package peaked at
645 MB of process tree. The run PRINTS the count with the limit that bound
it — "2 jobs" without "cores" says nothing:

```
mutants: 2 jobs (min(cores 8/3=2, free 21GB/2=10 (measured), cap 8) — cores)
```

Memory that cannot be READ is not memory that is absent: an unreadable reading
prints `ram unknown` and lets the cores decide alone. `--jobs` is gone from
both halves: the Go run derives its count from the box it is on, and no
caller may type a number for either.

**A shard that measured NOTHING is retried once, and waits for the box first.**
The trigger is the missing measurement, not the wording of the wreckage: a
shard whose exit status is not one cargo-mutants uses for a verdict (0, 2, 3),
or which wrote no `outcomes.json` while its `mutants.json` was not empty, buys
one retry on a cleaned build dir. The signatures that mean the machine rather
than the lane — `rustc-LLVM ERROR: out of memory`, `memory allocation of N
bytes failed`, os error 1455, `0xc0000142`, a rustc ICE, and the metadata
wreckage a killed compiler leaves — still NAME that failure in the log, because
"rustc ran the box out of memory" is a different instruction to an operator
than "it stopped"; they no longer decide whether the retry happens. They used
to, and a process killed before it could print a diagnostic matched none of
them: seven recorded escapes were one merge refused with `exited 4294967295 and
reached no verdict` on a tree the commit gate had just run green.

A shard that PRODUCED outcomes under a verdict status is a measurement and is
never retried, however alarming its log reads — re-running one is re-rolling it
until the box agrees with the lane. A shard drawn an empty slice (exit 0,
`mutants.json` as `[]`, no outcomes) had nothing to measure and is not retried
either. And it is one retry: a second no-verdict result refuses the merge
saying it was retried alone and reached no verdict again, so its mutants were
NOT measured. The retry used to start immediately,
which is right when the pressure was the run's own siblings and useless when
it is three other sessions' builds: it runs into the same wall. So it first
waits for the box to have room for the cold jobs it is about to start, up to
10 minutes, polling every 15 s and printing what it is waiting for. If the
room appears it retries at full width; if it does not, it retries at ONE
build job rather than not at all. An unreadable free-memory reading never
waits, and says so. Waiting can never buy a green: a shard that measures
nothing twice is reported as unmeasured, never as caught.

**One run per BOX, not one per repo.** The call is wrapped in a machine-wide
advisory lock held for its whole duration, cold build included. A wall-clock
test with `threads-required = "num-cpus"` has already declared that one run
needs every thread on the box, and four runs sharing it 4:1 measure nothing
usefully for any of them (issue #253). A second run queues, announcing who it
is waiting for about once a minute. There is no timeout on that wait.

**Nor while the box's CI runs.** The self-hosted GitHub runners share the box,
and their jobs take neither that lock nor a build slot. Each tool times its
mutants from a step it runs first (gremlins' coverage gather, cargo-mutants'
baseline), so a CI job that starts after that step turns healthy mutants into
timeouts: one run that overlapped two PRs' jobs reported 21 timed out of 35,
and the same tree on a quiet box caught 35 of 35. So once the lock is held and
before the tool starts, a run looks for busy runner jobs: `Runner.Worker`
processes, found by the basename of `argv[0]` in `/proc`, one per running job
beside each runner's always-running `Runner.Listener`. A job this process runs
inside is not counted, since the nightly workflow measures from within one.
When one is busy the run prints which, waits, and says again every minute;
after 15 minutes it measures anyway and says a timeout may be the box's load
(logged as `mutants-ci-busy`). A box with no busy job, or no `/proc`, waits
for nothing and prints nothing.

### Temp dirs, build dir, free space

The shard count is what the BOX allows, lowered by what the drive fits and
then by how many mutants the lane's diff actually has — asked of the tool
itself with `--list --json` over the run's own scoped argv, which builds
nothing. A shard that draws an empty slice is counted as empty rather than as
no-verdict, so that last term is wall clock rather than correctness: a
two-mutant diff used to start one cold baseline build per core. A probe that
cannot answer never lowers anything.

Three runs died at mutant 101 of 131 on a full disk: `TMPDIR` alone was
exported, a Windows cargo-mutants ignored it, and the tree copies took `C:`
from 40 GB free to 12 GB. So:

1. **All three** of `TMPDIR`, `TMP` and `TEMP` are set, on every platform, to
   the shard's own `<run temp>/shard-<i>/tmp`, where `<run temp>` is
   `.mutants/<worktree name>` beside the worktree, on its disk, never the OS
   temp dir. Setting one and inheriting the others is the bug: whichever name
   the tool reads is the one that decides.
2. `CARGO_BUILD_JOBS` is the RUN's total build width divided between its
   shards, both terms derived at runtime and the smaller winning:
   `min(cores, memGB/perJobGB)/shards`, floored at 1, where a job is priced
   at 2 GB warm and 6 GB cold — documented ESTIMATES rather than
   measurements. Cargo's own default is the whole machine, which for seven
   shards on a 24-core box is up to 168 rustc processes, so without this cap
   the run ends as an OOM or as swap thrash with every verdict it had
   reached lost. It is computed from the FINAL shard count, after the disk
   budget has reduced it, so a run cut to two shards uses the width two
   shards may safely use, and it is derived ONCE per run and handed to every
   shard — two shards of one measurement must not disagree about the machine
   they share. `mutants-build-jobs` overrides it verbatim for a box whose
   shape the derivation reads wrong, and the run logs which term decided:
   `mutants: 1 cargo build job per shard (min(cores 24, ram 63GB/6GB=10) —
   ram: 10 total across 7 shards, cold)`. The lone re-run passes one shard
   and gets the whole box, which is what having it to itself means.
3. **`memGB` is what is FREE, not what is installed.** Both budgets — the
   shard count `min(cores/3, memGB/8, 8)` and the build width above — take
   the SMALLER of total RAM and the memory the box can actually hand out
   right now: available commit from `GlobalMemoryStatusEx` on Windows (RAM
   plus pagefile minus everything already charged, which is the real ceiling
   on a box whose pagefile is pinned), `MemAvailable` on Linux. A run on a
   box with 25 of another session's cargo and rustc processes already
   compiling still derived seven shards from 63 GB of total RAM and lost six
   of its seven baselines to `rustc-LLVM ERROR: out of memory`. The reading
   may only ever LOWER the derived numbers — an idle box reports more
   available commit than it has RAM, and a budget that took that at its word
   would start more work on the strength of a number that can vanish — and a
   reading that cannot be taken is UNKNOWN, which falls back to total RAM
   rather than to a guess. Neither budget ever derives fewer than one shard
   or one job: a loaded box degrades to a slow correct run, never to no work.
   The report names the binding term and says it was measured: `mutants: 3
   shards (min(cores 24/3=8, free 24GB/8=3 (measured), cap 8) — free)`.
4. `CARGO_TARGET_DIR` is the shard's own `<run temp>/target-<i>` — never the
   lane's, which an editor's own builds compile into. That is what makes
   skipping the queue safe rather than merely faster: a mutation build owns
   its directory for hours behind cargo's own blocking lock. These
   directories are the run's one deliberate leftover, kept so the next run is
   warm in its DEPENDENCIES; the packages the run mutates are cleaned out of
   the directory before the shard starts, because those artifacts belong to
   the previous run's last mutant. `gate gc` reclaims the ones no live build
   owns.
5. Free space on that drive is MEASURED against what the run will actually
   put there BEFORE it starts: one copy of the tracked source tree per shard
   (`git ls-files`, since the copy is `--copy-target=false` and honours
   gitignore) plus what each shard's persistent `target-<i>` holds today —
   stat'd when that directory exists, and an estimate of 15 GB, reported as
   an estimate, for a shard that has never built. 10 GB is kept free on top.
   A drive that cannot carry every shard REDUCES the run to the shards that
   fit rather than refusing it; only a drive that cannot carry one refuses,
   naming the numbers, and logs `mutants-refused:disk`. A drive whose free
   space cannot be read never refuses and never reduces — this side's own
   blind spot must not stop a run that would have been fine.

   A Go run has no build dir to stat. Each gremlins worker is priced at the
   same tracked-tree copy plus 512 MB for the test binaries `go test` links
   and the tests' own scratch, reported as `estimated from a measured
   gremlins run`: a two-worker run over this repo's largest test package put
   a 6.1 MB copy and a 41-51 MB `go-build*` dir per worker on the drive, and
   its whole `.mutants/<lane>` area peaked at 174 MB.

   The guess this replaced multiplied the shard count by a flat 15 GB and had
   never stat'd anything: it asked for 105 GB against ~380 GB free and passed
   a run whose single copy needed 375,067,198,764 bytes.

`APHROLLO_MUTATION_GATE=1` marks the run's children. That one marker answers
both questions the cargo shim asks: a bare `cargo mutants` is refused and told
to type the verb instead, and a marked run builds without queueing behind
every editor on the box. `gate doctor` reports free space on the drive holding
each project's target dir and warns under 30 GB.

### Env-gated suites and the nextest profile

Mutants in code only a gated suite reaches are missed by definition: 101 of
167 mutants on one lane lived in render-world code reached only by GPU parity
tests. Each `mutants-env` entry is exported for the run (and each
`fail-first-env` entry for the fail-first proof, which has the same blind
spot), and
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

### Mutants nothing judged, on lines the diff adds

With `mutants-at-merge` on, a not-covered or inconclusive mutant on a line the
measured diff adds or changes (`git diff -U0 <base>`) refuses like a survivor,
named in the accept-list's own form (`internal/tdd/mutation/mutants_newlines.go`):

```
internal/cli/ci.go:50:27 CONDITIONALS_NEGATION (not covered)
torque/torque.go:5:16 ARITHMETIC_BASE (inconclusive) — UNRESOLVED: the tests of driveline did not finish within 2m0s
```

The same mutant on a line the diff did not touch is counted and reported, and
refuses nothing. A mutation-accept entry admits a refused one exactly as it
admits a survivor. Before the judge sees them, three kinds are taken out of
the refusal because no test could ever change them:

- a file this platform's build leaves out (its build constraints), which no
  run here compiles;
- a position outside every instrumented function body — a package-level
  `var` or `const` initializer, the body of a function named `_` — which Go
  coverage never attributes to a test;
- `ARITHMETIC_BASE` on a string concatenation, whose mutant cannot compile.

Two kinds are settled by running the one mutant, the way `gate mutants prove`
settles one (`internal/tdd/mutation/mutants_resolve.go`): a NOT COVERED at a
position Go coverage cannot count however often it runs — a case clause's
expressions, the rest of a statement after its first function literal
(`internal/tdd/mutation/mutants_covershape.go`, held to gremlins' own report
over a fully exercised module in `internal/tdd/mutation/testdata/covershape/`) — and every
inconclusive survivor. The mutant is swapped in through `go test -overlay`,
never written into the checkout, under the box-wide mutation lock.

**The own-package rule.** A mutant on a line the diff adds that its own
package's tests miss is refused at once, with no other package's tests run for
it: gremlins already ran the mutated package's tests over an inconclusive
survivor, so that survivor is a plain survivor. A NOT COVERED gap in such a
package runs its own package's tests alone. A package with no tests of its own
has none to miss the mutant, so it is settled against its importers like an
integration package.

**Integration packages.** A package listed in `mutants-integration-packages` is
settled the long way: its own package's tests first (skipped for an
inconclusive survivor), then the tested packages that import it one import
distance at a time, nearest first — the packages that import it directly, then
the packages that import those. Each distance is one `go test` over its
packages, and the run stops at the first kill, so a kill by a direct importer
never pays for the packages further up. Distance comes from each package's
direct imports and its tests' imports (`internal/tdd/suite/go_test_reach.go`).
A settle run has a total time cap (10 minutes for the whole run, cut down per
mutant from what is left); a mutant that finds the cap spent is UNRESOLVED.

A test failure is a kill and a build failure makes it unviable; both are
reported with their reason and refuse nothing. Green everywhere makes it a
survivor, judged against the accept-list like any other. A run past the
per-mutant budget (the same `max(3 × baseline, 120 s)` a Cargo mutant gets),
past the run's settle cap, or a reach graph that cannot be read, leaves it
UNRESOLVED: refused, saying so, and never called a survivor. The cure for one
is a test in the mutated package itself that kills it, which gremlins then
judges directly.

A settled mutant is named `<file>:<line>:<col> <MUTATOR>`, with no colon after
the column. The `<file>.go:<line>:<col>: <text>` shape is the one CI's Go
problem matcher turns into an `Error:` annotation, so only the mutants the
report refuses take it and every kill, unviable or unrefused inconclusive
mention stays a plain log line. A refused not-covered or inconclusive mutant is
printed twice: once in the annotation shape, then in the copyable accept-list
form.

### Measuring in CI

With `mutants-at-merge = "ci"` the measurement is the PR pipeline's
`mutants-verdict` job, on GitHub-hosted runners, and it is a required check.
Unless the repo pins `mutants-at-merge-level = "block"`, the local merge gate
prints `mutants: reported by CI (mutants-verdict), not waited for` and goes on;
the rest of this paragraph is the pinned case. The pinned gate prints
`mutants: measured in CI (mutants-verdict)`
(`internal/tdd/mutation/mutants_ci.go`) and refuses to merge unless that check
concluded `success` on the PR's head commit: no such check, one still running,
and one that failed or was skipped are each refused with the reason, and a
check that cannot be read (no `gh`, no GitHub remote, no network) is refused
too, since the merge is then not shown to be measured. `aphrollo gate mutants
run` and `prove` stay available by hand and say the repo measures in CI.

The pipeline divides the measurement across runners. A matrix of `shard` jobs
each runs `aphrollo gate mutants run --shard <i>/<n> --report <file>`: shard i
measures the changed source files it owns and writes its outcomes, judging
nothing (`internal/tdd/mutation/mutants_goshard.go`). Files are divided by
added lines, heaviest first, each to the lightest shard, so every shard
computes the same division from the same diff. A shard that owns no file writes
an empty report. The aggregate job, named `mutants-verdict` because branch
protection requires that name, downloads the reports and runs `aphrollo gate
mutants verdict --shards <n> <file>...`, which refuses unless exactly n reports
arrived, one per index, all of this tree and one base
(`internal/tdd/mutation/mutants_shardmerge.go`), and then judges the merged
outcomes once against the accept-list, so a survivor in any shard refuses. The
shard count is `mutants-shards` in `aphrollo.toml`.

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
**gremlins** (`internal/tdd/mutation/mutants_go.go`), scoped to the same base diff and
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
`mutants-unmeasured:gremlins-windows` and passes — saying in as many words
that the merge carries NO mutation evidence, which is a GAP rather than the
routine skip `nothing-to-measure` is (issue #697) — and Linux measures instead —
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

## At commit

With `mutants-at-commit = true` (or `"report"`) the commit gate measures the commit itself and reports each survivor by name without refusing the commit; `"block"` pins the old behaviour, where a survivor refuses it. The text below describes the measurement
(`internal/tdd/mutation/mutants_commit.go`), after the root's own checks have
passed and before the commit lands. The same run is `aphrollo gate mutants
commit` by hand. It is Go only, and it is a different measurement from the
pre-merge one: it mutates only the lines the staged change adds or changes,
and it runs each mutant against a selection of tests instead of the package.

**Which mutants.** The staged diff (`git diff --cached -U0`) names the added
lines. The mutants on them are enumerated by walking the same five node kinds
and the same token table gremlins uses for its default mutators
(`ARITHMETIC_BASE`, `CONDITIONALS_BOUNDARY`, `CONDITIONALS_NEGATION`,
`INCREMENT_DECREMENT`, `INVERT_NEGATIVES`), so a mutant here has the position
and the name CI's `mutants-verdict` gives it. gremlins' own dry run is not
used to list them: it gathers coverage for the whole module first, measured
at 3 min 17 s on this repo, which cannot sit on a commit's path. A position
outside every function declaration, a test file, test data (`testdata/**` and
a ratchet law's `.ratchet/fixtures/**`, which every mutation path leaves out:
this run, the CI measurement, `gate mutants run` and the test-map build),
vendored code, a file this platform's build leaves out and a file with unstaged edits are left
out (the copy the run works in holds the working tree, so a file whose working
copy is not what is staged would be judged as something the commit does not
hold).

**Which tests.** Each mutant runs `go test -overlay <mutant> -run <tests>
./<package>` in a disposable copy of the lane (the same copy `gate mutants
prove` makes, so a mutant that writes or resets acts on the copy), under the
memory cap every gate-started process is held to, with `-count=1 -failfast`
(`internal/tdd/mutation/mutants_commit_run.go`). The tests are the ones the
package's test map says execute the mutant's line (below). A mutant on a line
in a block no test executes is NOT MEASURED as `not-covered` and no `go test`
is started for it: no test could kill it. A mutant its covering tests miss is
a survivor at once, since a test that does not execute the line cannot be
affected by the mutant. A package with no map (its coverage could not be
measured), a line outside every block, or a `-run` pattern over 8000
characters falls back: the tests the commit touches (a test with an added line
in it; every test of a file whose helper, variable or type the commit changed;
a changed `TestMain` runs the whole package) run first, and a mutant they miss
is run against the rest of the package (`-skip` of the selected tests) before
it is called a survivor, because that selection is a guess. A failure is credited as a kill only when the
tests that failed pass without the mutant; that check is made once for a set of
failing tests and shared by every mutant of the run they fail under.

The run is in two passes over the same workers. The first runs every mutant's
selection (and nothing else), so a mutant a selected test kills in seconds is
never left waiting behind a whole-package run or cut by the budget because of
one. The second runs the whole package for each mutant the first did not
settle and whose selection was a guess (the fallback above). Each
worker keeps its copy of the lane at a path of its own that every run reuses
(`prove-<lane>/tree` for the first, `run-w<n>` for the others): the go build
cache keys a compile on the directory, so a copy at a new path rebuilds every
package the tested one imports (10 s of wall-clock and 25 s of CPU for
`internal/tdd/precommit`, against 1.3 s with the path reused). A survivor costs
one whole-package run, which is what its own-package rule asks; the runs of
several survivors are not folded into one binary, since two mutants applied
together can mask each other and a pass of the pair would not show that each
survives alone.

**The test map** (`internal/tdd/mutation/mutants_testmap.go`) answers which
tests execute which lines of one package. The commit that needs it builds it,
in the foreground, inside its own budget; nothing is built in the background
and nothing is prepared ahead of a commit: no hook starts a build after a
merge or a commit, no verb builds one by hand, and a repo that does not
declare `mutants-at-commit` never has one made. For each package the commit
touches the stage compiles the test binary once with coverage of the package
(`go test -c -covermode=set -coverpkg`), lists its tests, and runs each test
alone under `-test.coverprofile` on the stage's workers, in a disposable copy
of the lane. A solo run per test is what gives line-to-tests: one run of the
whole suite writes one profile that cannot say which test ran a block, so the
cost is one compile plus one pass over the tests, with a process start for
each. Each profile's blocks (file, first line, last line) are folded into one
list with the tests that executed each; a block no test executed is kept with
no tests, which is how a line is known to be uncovered. A package whose
coverage cannot be measured, or not in the time the budget leaves, is named
`NOT MEASURED` and its mutants fall back as above.

**The cache.** What a commit measures is kept for the next commit to the same
package. The key is the package's import path, the hash of everything its test
binary is built from (the content of its own Go files, tests included, and of
every package it imports inside the module) and the Go version and build
settings (`go env GOVERSION GOFLAGS GOOS GOARCH CGO_ENABLED GOEXPERIMENT`). A
map is kept in the repository's shared git directory
(`aphrollo-mutcover/`, one file per package and key), so every lane of the
repository reads what any of them measured, and a hit skips the compile and the
solo runs. A map of another key is never used, not even meanwhile: it is keyed
by line, and an edited line is another line. The directory holds at most 64
maps within 64 MiB (the ones read longest ago go first), and `aphrollo gate gc`
removes a map nothing has read for 30 days.


**The budget.** The run has `mutants-commit-budget` seconds of wall-clock,
counted from the start of the stage, on at most as many workers as the Go
measurement derives for the box (`min(cores/3, free memory/2 GB, 8)`), each
with its own copy of the lane. A mutant not started when the budget ends, and
a run cut by it, is NOT MEASURED; so is the coverage build when it does not fit. The stage also measures nothing when the box
has no memory headroom, or when another mutation run holds the box-wide lock
for the whole budget. None of these refuses the commit: each is printed as

```
gate precommit: mutants → NOT MEASURED (<why>) — this commit carries no commit-time mutation evidence; CI's mutants-verdict decides
```

and counted in the gate log as `mutants-unmeasured:commit-<kind>`. A mutant on a line no test executes is counted under the kind `not-covered`.

**The canary.** Every runner that starts test processes (this run, the merge
measurement, `gate mutants prove`, the test-map build) fingerprints the git
state a leaking test would touch before it starts and again when it ends
(`internal/tdd/mutation/mutants_canary.go`): the repository's config without
its `[branch]` stanzas, the branch names, the tip of `main` with the reflog
subjects behind it, the checked-out commit, the operator's global git config,
the registered worktrees that are neither a lane nor a gate checkout, and what
each worktree present at both ends has checked out. A difference refuses the
run's result with the change named and records an escape. Ordinary work on a
busy box is not a difference:

- A lane made or pruned: a direct child of
  `<parent of the primary>/.worktrees/<repo>`, with its branch. A branch is a
  lane's, whatever its name (`feat/x`, `fix/y`, `lane/x`), when a non-gate
  worktree has it checked out at either reading; a branch checked out nowhere
  is a leak.
- The gate's own checkouts: `gate-trunkpreview-*`, `gate-prmerge-*`,
  `gate-failfirst-*`, `failfirst-wt/` and the `.aphrollo-head-*` checkouts
  under the gate state's `head-wt` directory (one constant, read by the commit
  gate that creates them and by the canary).
- A merge landing on `main` (the new reflog entries on top are all `merge …`
  or `pull…`).
- The checked-out commit moving by the owner: forward onto commits on
  `origin/<trunk>` (a pull or a fast-forward); over commits recorded by the
  post-commit hook or, for a merge, the post-merge hook; or by a rebase or an
  amend, whose new commits the post-rewrite hook (`gate postrewrite`, in the
  global and per-repo git gate) recorded after the first reading. The record
  is per repository, so for a move that is no descendant only commits recorded
  after the first reading count: a reset or checkout onto a sibling lane's tip
  is a leak, and so is a move back onto a published commit.

A registration anywhere else, the run's temp areas under
`.worktrees/<repo>/gotmp` and `.mutants` included, a branch no worktree has
checked out, and a commit or a bare move of `main` are leaks.

An escape recorded for a leak is one issue per class: its fingerprint is the
repository (its shared git directory), the stage and the set of changed part
labels, never the refs or paths inside them.

**The verdict.** A survivor no `mutation-accept` entry admits refuses the
commit, named as the pre-merge report names it, with the counts and the
remedy:

```
gate precommit: mutants → REJECTED — a mutant of a line this commit adds survived its tests (12.3s, slowest mutant 4.1s)
internal/tdd/mutation/mutants_config.go:12:7: CONDITIONALS_BOUNDARY
mutants: 10 tested, 9 caught, 0 unviable, 1 missed (0 accepted), 0 unmeasured
```

**Edit time.** There is none. No edit hook, Bash hook or prompt starts a
mutation run, and no detached process measures, prepares or builds anything for
mutation: it runs at commit, in the foreground, and nowhere else.

## Gate-log tokens

Every verdict writes one line, so `gate stats` can answer how many merges the
stage refused and for what without re-running anything.

| token | meaning |
|---|---|
| `mutants-passed:tested=…,caught=…,unviable=…,missed=…,accepted=…,unmeasured=…,notcovered=…` | a run that reached a verdict and found nothing unaccepted |
| `mutants-refused:` + the same counts | a run that reached a verdict, found a survivor or an unmeasured mutant, and refused the commit or merge because the repo pins `block` |
| `mutants-reported:` + the same counts | the same finding where the repo pins nothing: it is named and the commit or merge went through. `gate stats` counts it under the `reported` reason, never as a red |
| `mutants-refused:disk` | the build drive cannot carry even one shard's copy and build dir |
| `mutants-refused:tree-changed` | the run left the working tree different from how it found it |
| `mutants-refused:git-failed` | git could not read the tree, so the tree that was measured cannot be compared with the one the run started from — the refusal carries git's own stderr |
| `mutants-refused:git-world-changed` | the canary found the repository's config, branches, main, a non-lane worktree registration or the global git config different after a commit-time run, so the run's result is not trusted |
| `mutants-refused:no-verdict` | an exit status cargo-mutants does not use for a verdict; the message names disk exhaustion as the probable cause when the drive is, at that moment, below what one measurement process needs |
| `mutants-refused:coverage-run-failed` | the Go runner's coverage gather failed on the lane's own tree — a build error or a failing test, with no box-side cause in go test's output; the refusal names each package go test failed and each test it named |
| `mutants-refused:config` | a retired key, or a `mutants-after` naming a file that is not there |
| `mutants-refused:no-lane-tip` | neither `MERGE_HEAD` nor `GIT_REFLOG_ACTION` named the branch coming in |
| `mutants-refused:runner-failed` | the runner never started, so nothing was measured |
| `mutants-passed:` / `mutants-refused:` + the same counts | the commit-time run reached a verdict (the same tokens the merge writes; the commit gate's run logs under the stage `precommit`) |
| `mutants-unmeasured:commit-budget` | the commit-time run left mutants NOT MEASURED because its wall-clock budget ended |
| `mutants-unmeasured:commit-runner` | the commit-time run could not set up or start a mutant (no copy of the lane, a source that could not be read, a `go test` that did not start) |
| `mutants-unmeasured:commit-unconfirmed` | a failure under a mutant was not shown to be its kill: the same tests fail without it |
| `mutants-unmeasured:commit-headroom` | the box had no memory headroom to start the commit-time run |
| `mutants-unmeasured:commit-lock` | another mutation run held the box-wide lock for the whole budget |
| `mutants-unmeasured:commit-diff` | git could not say what the commit adds |
| `mutants-skipped:not-go` | `mutants-at-commit` in a repo that is not a Go module |
| `mutants-skipped:nothing-to-measure` | the commit adds no line a mutant sits on |
| `mutants-skipped:not-declared` | the repo declares no `mutants-at-merge` |
| `mutants-skipped:catch-up` | trunk merged INTO a lane; nothing lands, so nothing is measured |
| `mutants-skipped:not-a-merge` | a conflicted cherry-pick or revert being concluded |
| `mutants-skipped:nothing-to-measure` | the diff named no mutable source |
| `mutants-unmeasured:coverage-run-box` | the Go runner's coverage gather failed because of the box — go test's output carries `signal: killed`, `no space left on device`, `cannot allocate memory` or `resource temporarily unavailable` — so nothing was measured and nothing is refused |
| `mutants-unmeasured:gremlins-windows` | the Go runner cannot measure on this platform, so this merge carries no mutation evidence at all — counted under `unmeasured:` in `gate stats`, never beside a routine skip |

A run that reached a verdict lands in the green/red columns of the `mutants`
row. One that never measured anything is counted by its reason alone: a red
there would read as "a mutant survived" and send a reader looking for one that
was never measured.
