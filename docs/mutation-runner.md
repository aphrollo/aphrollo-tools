# The mutation runner contract

This describes the mutation run `aphrollo gate mutants run` performs: where
it happens, what it may skip, what it must set for the tool it drives, and
how it reads the answer back. Much of the text below still describes the
retired arrangement in which a consuming repo owned the producer script and
the run left a signed receipt behind; the run is in this binary now and
nothing signs anything.

## Where a run happens

One worktree per LANE, `<parent>/.worktrees/<repo>/mutants/<lane-key>`,
checked out detached at the tip and `git reset --hard`ed between runs. One
build dir per REPO, `<parent>/.worktrees/<repo>/mutants/target`, shared by
every lane's tree.

**One worktree per lane.** A repo-wide tree was destructive with two lanes in
flight: a commit in either fires the post-commit hook, and the second run
checked the shared tree out to its own tip under whichever run was still
working in it (issue #221 — six attempts, four hours, no receipt for anyone).
Two lanes are two directories, keyed on the lane's own checkout path.

**One target dir per repo.** The build directory did not need to follow the
split. Cargo keys a workspace crate's artifacts on the path it was compiled
from, so lanes sharing one target dir share the dependency graph and keep their
own crates apart; and the collision a shared directory risked — two producers
linking into it at once — is ruled out by the box-wide run lock below. A target
per lane paid for that guarantee twice over: measured on borld 2026-09-05, nine
per-lane target dirs of 8.3-18.4 GB, and across 44 runs the unmutated baseline
builds cost 180 min against 116 min for every per-mutant rebuild put together.
A tree's own legacy `target` is reclaimed by the next run's sweep, never under
a live producer.

**One run per BOX, not one per repo.** Before the producer is invoked at all,
the gate takes a machine-wide advisory lock and holds it for the run's whole
duration — its own cold build included, not only its test phase. A wall-clock
test with `threads-required = "num-cpus"` has already declared that a single
run needs every thread on the box, and a cold build of the same crates from
two lanes at once has been observed to OOM rustc; four runs sharing the box
4:1 measure nothing usefully for any of them (issue #253). A second run on the
same machine queues rather than starting, announcing who it is waiting for
(and what repo) roughly once a minute while it does. There is no timeout on
this wait — a lane that abandoned it and ran anyway would recreate the exact
oversubscription the lock exists to prevent.

The runner is invoked in that worktree as `bash tools/mutation_gate.sh <base
sha>`, with:

| variable | meaning |
|---|---|
| `APHROLLO_MUTATION_GATE=1` | this run came through the gate. The cargo shim REFUSES `cargo mutants` without it, and lets a run carrying it past the build queue |
| `CARGO_TARGET_DIR` | the warm build dir. Do not override it |
| `APHROLLO_MUTANTS_ARGS` | the cargo-mutants flags the gate computed — pass them through VERBATIM |
| `APHROLLO_MUTANTS_DIFF` | the reduced lane diff the run is scoped to |
| `APHROLLO_MUTANTS_BASE` | the base sha the receipt must record |
| `APHROLLO_MUTANTS_ENV` | space-separated `NAME=VALUE` switches to set for the mutation run (see below) |
| `APHROLLO_MUTANTS_JOBS` | the concurrency cap the gate computed, with `APHROLLO_MUTANTS_JOBS_WHY` explaining it |
| `APHROLLO_MUTANTS_BASELINE_EXCLUDED` | how many `mutation-baseline-exclude` entries the gate folded into the nextest passthrough below |

A producer MUST append `$APHROLLO_MUTANTS_ARGS` to its own `cargo mutants`
invocation, verbatim and unmodified. A script that instead builds its own
fixed argv (its own `--in-diff`, its own `--package` list, no `--exclude-re`
at all) silently drops every one of these onto the floor: the touched-package
narrowing (which packages the unmutated BASELINE even builds), the
already-judged-mutant exclusion a restart depends on to avoid re-measuring
what an earlier attempt already caught, and the `mutation-baseline-exclude`
filterset a repo declared to keep a flaky wall-clock test from vetoing every
receipt. None of this fails loudly — cargo-mutants still runs, the receipt
still gets signed, and the ONLY visible difference is that the run measures
more than it needed to and never appears to shrink no matter how much of the
diff or the exclusion list grows (issue #423). The gate's own signal for this
failure mode is the `APHROLLO_MUTANTS_ARGS unread` warning: if it computed a
non-empty value here and the producer's own stdout/stderr shows no trace of
it, the gate logs one line naming the variable rather than staying silent —
worth checking whenever it appears, but its ABSENCE is not proof the args
were read, only that nothing looked wrong from the outside.

`APHROLLO_MUTANTS_ARGS` is always of the shape

```
--in-place --in-diff <diff> --test-tool=nextest [--baseline skip] [--package <name> ...] [--exclude-re <mutant> ...] [-- -E <filterset>]
```

- `--in-place` — mutate the warm worktree. NEVER let cargo-mutants copy the
  tree: the copies land in the OS temp dir, build cold, and nothing collects
  them (11 copies, ~135 MB each, measured on one box).
- `--baseline skip` appears only when the gate's own log shows this checkout's
  last commit-gate run green inside the window; the run must not skip the
  baseline on its own initiative.
- `--package <name>` entries are the lane's own touched crates, one flag per
  crate. This is what keeps the unmutated BASELINE scoped to the crates the
  lane actually touched: the commit gate already proved the whole tree green
  for this tip, so re-proving it here only widens the blast radius — a
  wall-clock test failing in some other lane's untouched crate must not veto
  this receipt (issue #251). Absent entirely when the touched-crate set could
  not be determined, which means run the whole workspace, same as before this
  flag existed — never pass `--package` for an empty or guessed set.
- `--exclude-re` entries are mutants an interrupted earlier attempt already
  judged. A restart measures what is left.
- `-- -E <filterset>` appears only when the repo declares
  `mutation-baseline-exclude`, and everything after `--` is cargo-mutants' own
  passthrough to the test tool — forwarded to the SAME `cargo nextest run` it
  invokes for the unmutated baseline and for every mutant, so one flag covers
  both (issue #265: a wall-clock test that fails under load with or without a
  mutation both vetoes a receipt for a reason the tree is not responsible for,
  and reads a mutant as CAUGHT when the load, not the mutation, is what
  failed it). A repo declares it under `[aphrollo]` in `aphrollo.toml`, or
  `[workspace.metadata.aphrollo]` in `Cargo.toml`, as a list of `"<nextest
  filter expression> # why"` entries — the same shape and validation
  `mutation-accept` uses, and an entry with no reason does not count:

  ```toml
  [workspace.metadata.aphrollo]
  mutation-baseline-exclude = [
    "test(conditioner_burst) # box-contended wall-clock test; fails under load whether or not a mutation is applied (issue #265)",
    "test(moving_bandwidth) # ditto",
  ]
  ```

  The gate combines every reasoned entry into one `not(<f1> + <f2> + ...)`
  filterset and reports how many entries made it up in
  `APHROLLO_MUTANTS_BASELINE_EXCLUDED` and in the receipt's own `excluded`
  field (see below) — visible in the artefact a merge reads, not only in
  config, since the whole risk of this feature is a repo quietly excluding
  its way to a green receipt. The runner may derive the same count itself by
  counting its own `mutation-baseline-exclude` entries, the way it already
  counts `mutation-accept` entries for `accepted`.

### The baseline and the mutants share one profile — cargo-mutants gives no way to split them

A repo cannot give the unmutated baseline `retries > 0` while keeping
`retries = 0` for every mutant. Checked directly against cargo-mutants
27.1.0's own source and config schema, not inferred:

- `NEXTEST_PROFILE` (the env var `--test-tool=nextest` runs under, and how
  `.config/nextest.toml`'s `[profile.mutants]` gets selected at all — confirmed
  against `cargo-nextest`'s own CLI, `dispatch/common.rs`: `--profile` is
  declared `env = "NEXTEST_PROFILE"`) is set ONCE, by the runner script,
  before `cargo mutants` starts, and cargo-mutants never touches it again: it
  is inherited unchanged into every child process the run spawns.
- Inside cargo-mutants, `cargo_argv`/`run_cargo` (`src/cargo.rs`) build the
  test-tool invocation from `Phase` (`Build`/`Check`/`Test`) and the global
  `Options` alone. Neither function takes the `Scenario` (`Baseline` vs.
  `Mutant(_)`) that `src/lab.rs`'s `run_baseline` and per-mutant runs both
  eventually call through — so the unmutated baseline and every mutant
  literally run the identical argv and environment for the test phase.
- `cargo mutants --emit-schema config` — the tool's own config schema — has
  no baseline-specific key: `additional_cargo_test_args`, `test_tool`,
  `timeout_multiplier`, all apply uniformly to `Phase::Test` regardless of
  scenario. There is no `--baseline-test-arg` or equivalent CLI flag either.

So: cargo-mutants does not expose the baseline run separately, categorically,
and nothing on the gate side of the call can make it do so — the two
questions ("does this test catch the mutation?" vs. "is the tree green
before we start?") share one process-wide test invocation because
cargo-mutants' own architecture shares it. The only way to actually decouple
them is to run the baseline OURSELVES, outside cargo-mutants, under a
different profile, then invoke `cargo mutants --baseline skip` — which
duplicates cargo-mutants' own build+test machinery and is not something the
gate does today.

Two remedies work without any of that, and neither needs a change here:

- **`mutation-baseline-exclude`** (above) already solves this for a NAMED
  test: excluded from both phases, it can never veto a receipt under load
  again. The cost is permanent: a mutant caught only by an excluded test is
  never caught again.
- **Make the test load-insensitive**, the fix issue #230 already used for
  `headless_glue`/`reconnect` (`.config/nextest.toml`'s own comment on
  `[profile.mutants]`): an ephemeral port, `threads-required = "num-cpus"`,
  or a dedicated `test-group` so the test stops competing with the run's own
  concurrent build for whatever makes it flaky. This is the only remedy that
  keeps full mutation coverage, and it is work in the consuming repo on the
  named test, never a runner-side setting.

A run that dies because the baseline failed already reaches this contract's
`died` state (no receipt ever written), not a receipt with a false verdict —
that distinction needs no fix here either.

## What the runner must do

1. Run cargo-mutants with `$APHROLLO_MUTANTS_ARGS` plus the timeout flags
   below, in the worktree it was invoked in.
2. Write the receipt JSON to
   `~/.claude/gate-state/mutation-receipt.<tip tree>.json`, where `<tip tree>`
   is `git rev-parse HEAD:`.
### Timeouts

Nine timeouts at 30 s were measured on one lane, every one of them a mutant
that was fine and a box that was busy — eight cold copies were compiling at
once. Contention must not read as a timeout, so the runner passes:

- `--minimum-test-timeout <T>` where `T = max(3 × measured baseline seconds,
  120)`. The baseline is the unmutated suite's own wall time; when the baseline
  was skipped, use the last recorded one, and 120 s when there is none.
- `--timeout-multiplier 3`.

A timeout is NOT a miss and is reported separately (`timeout` in the receipt).
A receipt with `timeout > 0` is REFUSED at merge, with `rerun with fewer jobs`
as the remedy: a timed-out mutant is an unmeasured mutant.

### Temp dirs and free space

Three runs died at mutant 101 of 131 on a full disk. The runner had exported
`TMPDIR`, which a Windows binary ignores, so cargo-mutants wrote its tree
copies to the OS temp dir on `C:` and took it from 40 GB free to 12 GB (98%).
Two rules follow, and the runner must obey both:

1. Export **all three** of `TMPDIR`, `TMP` and `TEMP`, on every platform,
   pointing at a directory under `$CARGO_TARGET_DIR` (the gate sets them for
   the runner it starts; a runner invoked by hand sets them itself). Setting
   one and inheriting the others is the bug: whichever name the tool reads is
   the one that decides.
2. Check free space on the drive holding `$CARGO_TARGET_DIR` BEFORE starting,
   against `jobs × 15 GB`, and exit with one line naming both numbers when it
   is short. The gate does the same check before it starts a run and logs
   `mutants-refused:disk`.

`gate doctor` reports free space on the drive holding each project's target
dir and on the OS temp dir's, and warns under 30 GB.

### Concurrency

`APHROLLO_MUTANTS_JOBS` defaults to `min(cores / 6, RAM_GB / 6, 2)`, floored
at 1, and the runner PRINTS the cap and the reason it was derived from — the
`— <reason>` suffix included, because "2 jobs" without "cap 2" or "ram" says
nothing about which limit bound it:

```
mutants: 2 jobs (min(cores 24/6=4, ram 64GB/6=10, cap 2) — cap 2)
```

Memory that cannot be READ is not memory that is absent: an unreadable reading
prints `ram unknown` and lets the cores decide alone, rather than pinning the
run to one job.

The gate computes the same number and passes it as `APHROLLO_MUTANTS_JOBS`
with the reason in `APHROLLO_MUTANTS_JOBS_WHY`; a runner may use those instead
of computing its own.

### Env-gated suites

Mutants in code only reached by an env-gated suite are missed by definition:
101 of 167 mutants on one lane were in render-world code reached only by GPU
parity tests. The repo names the switches its mutation run must set:

```toml
[workspace.metadata.aphrollo]
mutants-env = ["FORGE_GPU_TESTS=1"]
```

The gate passes them to the runner in `APHROLLO_MUTANTS_ENV`, and the runner
exports each one for the mutation run. Every switch named there must be
registered in the repo's dev-instrument registry — an env switch that gates a
suite is exactly the kind the registry law exists to catch.

### Tests that read the source tree at run time

Mutants run in a COPY of the tree, so a test that resolves a path at RUN time
rather than at compile time is reading a directory that no longer exists. The
failure looks nothing like the cause:

```
FAIL client anim::driver::tests::bone_map_is_only_referenced_by_driver_setup_and_tests
panicked at crates/client/src/anim/driver.rs:345:
Os { code: 3, kind: NotFound }
ERROR cargo test failed in an unmutated tree, so no mutants were tested
```

That test walked `CARGO_MANIFEST_DIR` at run time to assert which files
reference a symbol. It passes everywhere else and can never pass under a
mutation run. The reported error says the unmutated tree is broken, which
sends a reader looking for a regression that is not there.

The diagnostic: a baseline failure whose error is a missing PATH, rather than
a failed assertion, is this shape almost every time.

Anything resolved through `CARGO_MANIFEST_DIR` at execution — the crate's own
sources, the workspace manifest, `.ratchet/` data, a fixture directory — is
incompatible with the copy tree. This is cargo-mutants' design and not
something the gate can paper over.

The fix is placement, not a workaround. A check that observes FILES ON DISK is
a law, under `.ratchet/laws/`, where it runs against the real tree once per
commit instead of once per mutant. `bone_map_scope` is that case, resolved
that way. Where a test genuinely must stay in-crate, embed what it needs at
compile time (`include_str!`, `include_dir!`) so no path is resolved at run
time.

## Go repos

A repo whose worktree has a `go.mod` is measured with **gremlins**, and the
choice was measured rather than argued:

| tool | on this box |
|---|---|
| gremlins v0.6.0 | installs and runs; `--diff <ref>` scopes to the lane, `--output` writes machine-readable results, `--workers` caps concurrency. Analysis of `internal/tdd`: 1626 runnable mutants, 270 not covered, 85.76% mutator coverage, 2.6 s behind one full coverage run |
| go-mutesting | does not BUILD on windows/amd64 — its `zimmski/osutil` dependency uses `syscall.Dup` and `syscall.RLIMIT_NOFILE`, neither of which exists there. No wall time to compare |

gremlins' statuses map onto the receipt as: `KILLED` → caught, `LIVED` →
missed, `NOT COVERED` → not_covered (never a survivor on its own — no test
ran, so nothing "did not notice"), `TIMED OUT` → timeout, anything else →
unviable. An unrecognised status is never read as caught. The outcome
store's own schema is bumped whenever this mapping changes, so a store
written under an older one is discarded rather than replayed under the new
meaning.

The diff-only pilot that preceded this wiring found the signal clean on this
repo (4/4 survivors were real gaps, 0 equivalent-mutant noise) and surfaced
one standing gap worth naming: gremlins gathers coverage from the UNIT suite
only, so a repo whose real logic sits behind `//go:build integration` tests
(a Postgres-backed store package, say) gets an empty or misleadingly-clean
report over that code unless the run adds `--tags integration --integration`
— which also re-runs the full suite per mutant, so it is a deliberate,
slower opt-in rather than the default shape above.

The same pilot named one operational hazard that follows from how that
coverage is gathered: gremlins runs the WHOLE module's test suite once before
it mutates anything, so a single failing or flaky test anywhere in the module
aborts the entire run with `failed to gather coverage` — a rust-analyzer e2e
flake did exactly that here. The failure names coverage rather than the test
that caused it, which is the part worth knowing in advance. This repo's own CI
does not hit it because the `mutants` job declares `needs: [changes, test]`
and so cannot start over a red module, but that ordering was chosen to avoid a
git-config lock collision and only incidentally covers this; a pipeline that
runs `aphrollo gate mutants go` without a green suite ahead of it gets the
abort with no hint of the cause.

The accept-list for survivors lives in `aphrollo.toml`, with a reason per
entry — an entry with no reason does not count as accepted:

```toml
[aphrollo]
mutation-accept = [
  "internal/tdd/gc.go:120 CONDITIONALS_BOUNDARY # the bar is the sweep's own, pinned by the sweep test",
]
```

An entry may also name a column — `<file>:<line>:<col> <MUTATOR> # why` —
which is the only way to accept one of several mutants sharing a line: a real
receipt carried three distinct survivors on
`crates/editor_client/src/creator.rs:108` at columns 5, 33 and 71 (issue
#282). A column-less entry matches by file, line and mutator alone, which is
fine for the overwhelmingly common case of one mutant per line and keeps
every accept-list written before this existed working unchanged; the gate
refuses to APPLY a column-less entry to a line that turns out to carry more
than one mutant, so an unexamined sibling stays unaccepted and blocks rather
than being silently admitted alongside the one somebody actually reviewed.

## How a mutant is named

cargo-mutants 27.1.0 names a mutant `<file>:<line>:<col>: <mutation>`:

```
crates/editor_client/src/creator.rs:101:33: replace || with && in send_undo_redo
crates/editor_client/src/creator.rs:101:16: replace || with && in send_undo_redo
```

Those are two DIFFERENT mutants. The column is part of the identity, not
decoration: that file emits several distinct mutants on line 101 with
identical text. So an accept-list entry and a re-run's own name filter both
carry the column, and both use the tool's own line VERBATIM, anchored:

```
--exclude-re '^crates/editor_client/src/creator\.rs:101:33: replace \|\| with && in send_undo_redo$'
```

A rebuilt name that drops the column matches nothing, so the filter selects
nothing and the run measures the whole diff again. The list is also bounded
(8,000 characters of arguments): every name rides in the run's own argument
block, Windows caps that block at 32,767, and an overflowing list truncates
the arguments themselves.
