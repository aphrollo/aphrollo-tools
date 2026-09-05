# The mutation runner contract

This is the spec a consuming repo's own mutation runner (borld: the script at
`bash tools/mutation_gate.sh`, which lives in THAT repo, not this one)
implements. The gate owns the LIFECYCLE — when a run
starts, where it happens, what it may skip, and whether the answer is
trustworthy. The runner owns what only the repo knows: which mutants are worth
generating, which crates they belong to, and which survivors have been
accepted.

Nothing here is required for a run to work. A runner that ignores every
variable below still produces a receipt the gate accepts; it just does a full
run every time and its receipt is counted as unsigned (`receipt-unsigned`) and
unpinned (`receipt-unpinned`) rather than verified. The gate never rejects a
receipt for a field a runner has not caught up with yet.

## How a run starts

`aphrollo gate postcommit` (the `post-commit` git hook) starts the run when all
of these hold:

- the commit is NOT on `main`/`master`;
- the repo opts in — `[workspace.metadata.aphrollo] mutation-receipt = true` in
  the Cargo workspace manifest, or `[aphrollo] mutation-receipt = true` in a
  repo-root `aphrollo.toml` for a repo with no `Cargo.toml`.

The job is started DETACHED, below normal priority, and its stdout and stderr
go to files under the mutation worktree's build dir. It is never cancelled by a
later commit: a superseded run finishes, and its outcomes are what the next
run carries instead of re-measuring.

### Starting one by hand

`aphrollo gate mutants run`, typed in the lane, with no arguments. It builds
the same job the hook builds — same worktree, same base, same lock — and runs
it in the FOREGROUND so the output is on the terminal rather than in a log
file.

Do not invoke the repo's own producer directly. The lock is held by the gate
around the producer call, not by the producer itself, so a script invoked by
hand runs outside it; it also runs in whatever checkout it was typed in rather
than the mutation worktree, and puts its scratch wherever the ambient
`CARGO_TARGET_DIR` points. The producer is named in refusal messages as what
the run will drive, never as the command to type.

Two things `run` will refuse rather than do:

- **A second run while one is already going for this repo.** Both would measure
  the same mutants for the same receipt, and the box-wide lock would serialize
  them, so the waste would be quiet rather than absent. The refusal names the
  branch, pid and start time of the run already going.
- **Anything on `main`/`master`, in a repo that has not opted in, or in a repo
  whose mutants run in CI.** These are refusals the post-commit hook makes
  silently, because it fires after every commit on the box. Typed by hand they
  are printed, because there the refusal is the answer to what was just asked.

A run measures the tip's TREE, and a receipt is keyed on that tree. To re-run
against the tree a merge will land on, `git commit --allow-empty` on the lane:
an empty commit preserves the tree, so the run measures exactly what the merge
gate will check.

In the normal case, do not check whether a receipt has arrived at all. Attempt
the merge: the pre-merge gate consumes the receipt on its own and names
whatever is missing, including whether a run is still going and when it
started. `aphrollo gate mutants status` (below) exists for when someone
genuinely wants to watch, or needs to answer "why is nothing happening" — not
as a step in the routine path.

### Asking instead of polling

`aphrollo gate mutants status` answers for the checkout it is standing in —
never for any other lane — and reads exactly the files the pre-merge gate
itself reads (`RunningMutantsJobs`, the death record, the receipt), so the two
can never disagree about the same tree:

- **no run for this tree** — nothing has ever measured it: no receipt, no
  running job, no death record.
- **going** — a job for this repo is alive, naming its branch, pid and start
  time, and saying whether it is still queued behind the box-wide
  mutation-run lock (naming the holder) rather than actually measuring.
- **died** — the run ended without ever writing a receipt: the exit code and
  the log to read.
- **done** — a receipt for this exact tree is on disk: its verdict and every
  count (`mutants_total`, `caught`, `timeout`, `unviable`, `not_covered`,
  `excluded`, `accepted`, `unaccepted`).

`--wait` blocks the CALLING PROCESS on the running job's own pid until this
tree reaches a terminal state (died or done — "no run" and "already done" are
already terminal and return at once), then prints the same answer. It is a
real block on the process, never a loop sampling the receipt file on an
interval: the twenty-line polling loop this verb replaces (fixed 2700 s
timeout, a hard-coded receipt path, grepping the JSON by hand) should never be
written again.

Exit codes, so a script can tell every state apart without parsing the text:

| code | meaning |
|---|---|
| 0 | a receipt exists and would merge (verdict `pass`, no unaccepted survivor, no timeout) |
| 1 | the checkout itself could not be read (not a git repository, HEAD names no tree) |
| 2 | bad flags |
| 3 | no run has ever been started for this tree |
| 4 | a run is going right now (`status` only — `--wait` never returns this) |
| 5 | the run ended without ever writing a receipt |
| 6 | a receipt exists but would NOT merge (bad verdict, unaccepted survivor, or timeout) |

`status` does not repeat the pre-merge gate's repo-identity, base-sha or MAC
checks — those are about which MERGE a receipt is for, not what its counts
say, and receipt 4/5/6 for the same field said one way is what a script and
the merge gate both need to agree on.

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
| `MUTATION_GATE=1` | this run came through the gate. The cargo shim REFUSES `cargo mutants` without it |
| `APHROLLO_QUEUE=bypass` | skip the build queue. Honoured only while `CARGO_TARGET_DIR` is inside the mutants worktree |
| `CARGO_TARGET_DIR` | the warm build dir. Do not override it |
| `APHROLLO_MUTANTS_ARGS` | the cargo-mutants flags the gate computed — pass them through VERBATIM |
| `APHROLLO_MUTANTS_DIFF` | the reduced lane diff the run is scoped to |
| `APHROLLO_MUTANTS_BASE` | the base sha the receipt must record |
| `APHROLLO_MUTANTS_ENV` | space-separated `NAME=VALUE` switches to set for the mutation run (see below) |
| `APHROLLO_MUTANTS_JOBS` | the concurrency cap the gate computed, with `APHROLLO_MUTANTS_JOBS_WHY` explaining it |
| `APHROLLO_MUTANTS_BASELINE_EXCLUDED` | how many `mutation-baseline-exclude` entries the gate folded into the nextest passthrough below |

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

## What the runner must do

1. Run cargo-mutants with `$APHROLLO_MUTANTS_ARGS` plus the timeout flags
   below, in the worktree it was invoked in.
2. Write the receipt JSON to
   `~/.claude/gate-state/mutation-receipt.<tip tree>.json`, where `<tip tree>`
   is `git rev-parse HEAD:`.
3. Sign it — and ONLY through the signer:

   ```sh
   aphrollo gate receipt sign --outcomes mutants.out/outcomes.json \
       "$HOME/.claude/gate-state/mutation-receipt.$TIP_TREE.json"
   ```

   The signer stamps `outcomes_sha` and the `mac`, keyed by
   `~/.claude/gate-state/receipt.key` (created by `gate init`, mode 0600). It
   is the only writer of a receipt's `mac`. A receipt whose `mac` does not
   verify is refused at merge with `gate: receipt not written by
   tools/mutation_gate.sh` and logged `receipt-forged`; a receipt with NO `mac`
   is still accepted for now, and counted.

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

## Which merges a receipt gates

A receipt proves a LANE was measured before it lands on main, and that is the
only direction it is asked about. `pre-merge-commit` judges one when HEAD is
the repo's main branch and the incoming branch is not already contained by it;
anything else is a CATCH-UP merge, runs the mechanical stages only, and is
logged `catchup-merge`.

Refusing a catch-up cost more than it protected: `git merge main` inside a lane
worktree was blocked for a missing receipt against MAIN's tip tree, so the
builder squash-merged instead — which polluted the lane's merge-base diff with
all of main's changes, and every later mutation run had to measure them.

## Go repos

A repo with no runner script of its own gets the built-in one:
`aphrollo gate mutants go --job <file>`, which the job runner picks when the
worktree has a `go.mod` and no `bash tools/mutation_gate.sh`. It drives
**gremlins**, and the choice was measured rather than argued:

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

## The receipt

```jsonc
{
  "repo": "<git rev-parse --path-format=absolute --git-common-dir>",
  "branch": "lane/x",
  "tip_tree": "<git rev-parse HEAD:>",
  "worktree_dirty": false,      // git status --porcelain --untracked-files=no
  "base_ref": "origin/main",
  "base_sha": "<resolved sha the diff was taken against>",
  "mutants_total": 12,
  "caught": 11,
  "timeout": 0,
  "unviable": 1,
  "excluded": 2,               // count of mutation-baseline-exclude entries applied, 0/omitted when none declared
  "survivors":  [{"file": "…", "line": 12, "col": 33, "mutation": "…"}],
  "accepted": 1,
  "unaccepted": [],
  "verdict": "pass",
  "finished_at": "2026-09-03T10:00:00Z",

  // incremental: what the NEXT run narrows its diff with
  "files":     {"crates/a/src/lib.rs": "<blob>"},
  "fences":    {"crates/a": "<hash: see The fence>"},
  "outcomes":  [{"file": "…", "line": 12, "col": 33, "mutation": "…",
                 "name": "crates/a/src/lib.rs:12:33: replace || with && in f",
                 "status": "caught",
                 "package": "crates/a", "blob": "<blob>", "fence": "<hash>"}],

  // written by the signer, never by the runner
  "outcomes_sha": "<sha256 of mutants.out/outcomes.json>",
  "mac": "<hmac-sha256 over the canonical body>"
}
```

`worktree_dirty` is computed with `--untracked-files=no`: the run's own build
dir and logs are untracked by design, and counting them would make every run
report itself dirty.

### How a mutant is named

cargo-mutants 27.1.0 names a mutant `<file>:<line>:<col>: <mutation>`:

```
crates/editor_client/src/creator.rs:101:33: replace || with && in send_undo_redo
crates/editor_client/src/creator.rs:101:16: replace || with && in send_undo_redo
```

Those are two DIFFERENT mutants. The column is part of the identity, not
decoration — that file emits several distinct mutants on line 101 with
identical text — so the receipt carries `col`, the cache keys on it, and a
resumed run's `--exclude-re` is the tool's own line VERBATIM, anchored:

```
--exclude-re '^crates/editor_client/src/creator\.rs:101:33: replace \|\| with && in send_undo_redo$'
```

A rebuilt name that drops the column matches nothing, so the resume excludes
nothing and measures the whole diff again. The exclusion list is also bounded
(8,000 characters of arguments): every exclusion rides in one environment
variable, Windows caps the block at 32,767, and an overflowing list truncates
the run's own arguments. Past the bound the remaining mutants are simply
re-measured.

### Moved code is not changed code

A crate-topology lane MOVES code: a module leaves one crate and arrives in
another, byte for byte, with no behaviour change. To `--in-diff` every one of
those lines is a changed line, so such a lane would mutate thousands of lines
nobody touched.

git already knows which lines only moved, so the lane diff is taken with move
detection on and every moved line is dropped before the diff reaches the
runner:

```
git diff --color=always -M --color-moved=plain     --color-moved-ws=allow-indentation-change <base> <tip> -- <files>
```

A moved ADDED line becomes context — it is in the new file, it is not worth
mutating, and keeping it holds the file's line numbering where a mutant's
identity expects it. A moved REMOVED line is dropped outright. A hunk left with
no real addition goes, and so does a file left with no hunk. The colours are
pinned on the command line rather than read from the box's git config, because
the filter parses them.

A lane that is 100% moves therefore runs no mutants at all: the gate writes and
signs a receipt with `mutants_total: 0`, `verdict: "pass"` and `moved_lines: N`,
and logs `mutants-all-moved:<tree>`. The count is what makes a zero-mutant
receipt readable — it says why it is zero.

### The fence

A verdict is invalidated by anything whose change could flip it, which is more
than the mutant's own file: a mutant in `a.rs` may be caught only through
`b.rs`'s behaviour, and `b.rs` may live in another crate. So each package
carries a FENCE — one hash over

- the package's own Source blobs, and
- the package's own Test blobs, and
- the Source blobs of every transitive workspace dependency

— and an outcome carries the fence it was measured behind. Cached outcomes
carry only while both the file blob and the fence still match. The graph comes
from the build tool itself: `cargo metadata --no-deps` (path dependencies
only; a registry crate cannot change under a lane) or `go list`. No graph
means no edges, which fences every package by its own files alone — it
under-carries rather than over-carries.

### The outcome cache

A verdict is a fact about a BLOB and a TEST SET, not about a branch, so the
carry source is a repo-wide store rather than the last receipt:
`<gate-state>/mutants/<repo-token>/outcomes.json`, schema-stamped and written
whole. Every finished run merges its outcomes into it keyed by mutant, newest
verdict winning, and every plan carries from it whenever the file blob and the
package's test-set hash both still match — whichever lane measured them. Two
lanes touching the same file at the same blob therefore measure it once
between them.

The carry key is the CONTENT, not the path: an outcome is looked up first by
`<file>:<line>:<col>: <mutation>` and then by `<blob>:<line>:<col>:
<mutation>`, so a file that only moved carries every one of its verdicts. The
fence still has to match, so a move INTO another package — where different
tests constrain it — is re-measured.

The receipt stays the per-tip proof a merge consumes; the store is only the
cache. `gate gc --apply` prunes entries older than 30 days or naming a blob the
repo's object store no longer has.

When the cache answers for EVERY file the lane touched, no runner is started at
all: the gate writes and signs the carried receipt itself and logs
`mutants-fully-carried:<tree>`. That path exists because the alternative was
worse than useless — with no files left to scope it to, the run's diff was
`git diff BASE TIP --` with no pathspec, which is the whole lane diff, so the
run re-measured exactly what the cache had just excluded.

A receipt with no `outcomes` costs the next run a full re-measure and nothing
else. A receipt with `unaccepted` non-empty never merges. The accept-list for
survivors lives with the repo — Cargo metadata for Rust, `[aphrollo]` in
`aphrollo.toml` for Go — with a reason per entry.
