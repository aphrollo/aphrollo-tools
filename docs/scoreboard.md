# Gate capability scoreboard

An honest 1-10 per capability, each score carrying the command that re-derives it.

This exists because the project had no answer to "is this getting better or
worse?" other than impressions, and impressions were disagreeing with the
measurements. A stage everyone assumed was load-bearing turned out to have
caught nothing in 2353 runs, and nobody knew, because nobody had asked the
question in a form the tool could answer.

## Scope

This table scores the **gate** — hooks, stages, laws, escapes, receipts. It does
not score `refactor` (LSP rename, outline, show), the workspace verbs, `sqlc`,
the docs check, or the ratchet fixture tooling. Those surfaces have no rows yet,
so the overall figure below is the gate's score and not the tool's. Adding rows
for them is worth more than refining the ones here.

## How to use this

A score moves only when a command in the "re-measure" column produces a
different number. Rows marked `[judgment]` have no such command yet; that is
itself a finding, and converting a `[judgment]` row into a measured one is worth
more than arguing about its number.

**Measure, never quote.** The first draft of this table said `internal/tdd` had
zero parallel tests and that Windows CI skipped the package entirely. Both were
false. The first came from repeating an issue body instead of running the count;
the second contradicted a workflow job added to this repo the same day. Every
row below was re-derived from its own command before being written down, and the
next revision owes the same.

Re-score deliberately — at a milestone, or after a change large enough to make a
row's evidence stale. Not every session. Add a new dated column rather than
overwriting the old one, so the direction of travel stays visible.

The scale is calibrated against this repo's own capabilities, not against
software in general: 9 means "does its job and the evidence says so", 5 means
"the idea is right and the execution has a named hole", 2 means "actively costs
more than it returns".

## Baseline: 2026-09-07

| capability | score | evidence | re-measure |
|---|---|---|---|
| Post-edit hook | 9 | Caught all 2801 real assertion failures over 90 days, seconds after the edit that caused each one. This is the product; the rest is scaffolding around it. | `aphrollo gate stats --since 90d`, postedit red count |
| `commit-msg` hook | 9 | 7 rejections over 90 days, each quoting the offending line. One job, done. | `aphrollo gate stats --since 90d`, commit-msg rejection count |
| `gate stats` | 8 | The reason the rest of this table can exist: self-instrumentation good enough to indict one of its own stages. precommit 2353 runs / 0 red / 58 rejections is what retired the commit-time suite. Not a 9 because it counts what each stage *rejected*, which only measures catches for stages that report red — fail-first, ratchet and the mutation runner have no equivalent number. | `aphrollo gate stats --since 90d` |
| Queue shim (cargo/git) | 8 | Queues visibly behind another build instead of blocking on a silent lock. No incidents across a multi-day, multi-session run. | `which cargo` resolves under `cargo-queue` |
| Worktree guardrail | 8 | Merge-only primary held with no bypasses. Also refuses an unlabelled `git stash`, which is a real trap: `refs/stash` is shared across every worktree of a repo, and only a message says what an entry holds (#384). | attempt a non-merge commit in the primary checkout |
| Cheap precommit stages | 8 | Baseline, ratchet, docs, suppression, fmt, clippy and `cargo check` are fast and catch real defects — a `#[cfg(test)]`-only change failed `cargo check --workspace --tests` at commit time on 2026-09-07. These are what remains of the commit gate and they earn their place. | `aphrollo gate stats --since 90d`, per-stage rejection counts |
| Fail-first RED proof | 7 `[judgment]` | Re-proves a staged test goes RED at HEAD with only the staged test files applied. Mechanically sound, and conceptually the strongest idea here. Scored on judgment because **nothing counts how often it catches a bogus test** — the same blind spot that let the suite stage survive 2353 useless runs. | none yet; instrumenting this is the highest-value work this table points at |
| Ratchet laws | 6 | 27 laws over 2026 files, 0 regressions. Good design: declarative, baselines only ever go down, a new hit admitted by an escape comment rather than by editing a baseline. Two named holes — a law whose `matcher.kind` is unrecognized parses without error and then enforces nothing (#538), and `test_removed` cannot distinguish a rename from a deletion, so a pure rename demands one tombstone per test. | `aphrollo ratchet check` |
| Fuzzing | 6 | Harnesses exist and find real bugs, which is the hard part. Two crashes have been open unfixed for days (#537, #538), which is the easy part. | `gh issue list --label fuzz --state open` |
| Its own test suite | 5 | 1477 test functions in `internal/tdd`, 320 `t.Parallel()` calls across 70 files, 927 s wall clock for the package. Hosted CI runs it on Linux only, in the `gate-env` job (`.github/workflows/pipeline.yml`, `go test ./internal/tdd/... -count=1 -timeout=1500s`), gated on a code-change output; the hosted Windows jobs that used to cover it (`test-windows`'s allow-list excluded this package, `gate-env`'s windows leg included it) were removed to stop spending hosted Windows minutes. Windows coverage is now the pre-merge gate on the developer's own box, which is scoped to the packages a merge stages -- so a merge touching nothing here does not re-run it on Windows. The measured 927 s against the 1500 s budget is the remaining headroom. | `go test ./internal/tdd/ -count=1 -timeout=1800s` for wall clock; `grep -ho 't\.Parallel()' internal/tdd/*_test.go \| wc -l` for parallelism; read the `gate-env` job in `.github/workflows/pipeline.yml` |
| Escape tracking | 5 | The concept is the best governance idea here: a CI red after a local green becomes an issue closable only by naming a law, a stage or a test — never by a sentence in a document. The execution is thin. Of 3 open escapes on this date, one had been fixed nine hours earlier and stayed open, and two recorded the same failing test twice under different fingerprints. Records faithfully; does not deduplicate by failing test, and does not notice when its own fix lands. | `gh issue list --repo aphrollo/aphrollo-tools --label escape --state open`, then compare each fingerprint against its failing test |
| Generated instructional text | 5 | `doctor`'s entire purpose is telling an operator what to run next, and it emits verb spellings that are retiring (#551: 11 sites in `doctor.go`, plus `sessionline.go:189`). The mechanization to prevent this already exists — #518's test derives the alias set from the verb tables rather than hardcoding it — it was simply never pointed at these surfaces. | `gh issue view 551` |
| Windows behaviour | 4 | A `GOTMPDIR` given with forward slashes broke 40 tests in one run on 2026-09-07, and `cargoWorkspaceRoot` returns a path mixing both separators. Windows is the primary development platform for this tool's users and is its least defended surface. | run the suite with `GOTMPDIR` set to a forward-slash path and count failures |
| Mutation receipts | 3 | Knows, and signs anyway. `mutantsArgsUnreadWarning` correctly detects a producer that ignores `APHROLLO_MUTANTS_ARGS` by teeing the producer's output and checking whether the computed args ever appear (#423) — and the consuming repo's runner confirms in a comment that it does not read the variable. But the detector only `logf`s. The run proceeds and writes a signed receipt indistinguishable from a clean one, which the merge gate then consumes, after the gate has already observed that its own scoping and baseline-exclusion flags were dropped. Not a 2 only because the detection is real and the fix has a hook point waiting for it. | in the consuming repo, `grep -n APHROLLO_MUTANTS_ARGS` in its mutation runner; then check whether a receipt records the warning |
| Precommit mechanical suite | 2 | 2353 runs, 0 assertion failures caught, 58 commits rejected for exceeding the 600 s timeout. Pure tax, measured over 90 days. Removed on this date; the merge gate still runs the same suite, with `-race`. | `aphrollo gate stats --since 90d`; the tripwire is premergecommit red, 0 of 1077 at baseline |

**Overall: 6.5 / 10 for the gate.** The tool's own figure is unknown until the
unscored surfaces above have rows.

## What the shape says

Sorting by score separates the gate into three layers, and they are in very
different health.

**Measurement is the strongest layer.** The post-edit and `commit-msg` hooks and
`gate stats` all score 8 or 9 on their own numbers. Being able to ask "has this
stage ever caught anything" and get a count back is rare, and it is why a
90-day-old assumption could be retired in an afternoon rather than argued about
indefinitely.

**Enforcement is good and improving.** The cheap commit stages, the worktree
guardrail and the queue shim all hold. Ratchet is the weakest at 6, and its holes
are specific and fixable rather than structural.

**Certification is the weak layer.** Mutation receipts at 3 and escape tracking
at 5 are both mechanisms that observe correctly and then fail to act on what they
observed: the mutation run warns that its filter was dropped and signs the
receipt anyway; the escape tracker records a miss and never notices the fix. Both
produce artifacts a human trusts and then stops checking. That is the specific
danger — a wrong measurement is worse than no measurement, because it ends the
investigation.

## What to fix first

Make the mutation receipt refuse to certify what it has already warned about.
`mutantsArgsUnreadWarning` fires today and only logs; the run then writes a
receipt a merge gate cannot distinguish from a clean one. Marking that receipt
unscoped is a small change at a point the code already reaches, and it converts a
document that can assert something untrue into one that cannot.

Second, instrument the fail-first RED proof. It is the one high-scoring
capability resting on judgment, and the suite stage is the standing proof of what
happens to an uninstrumented stage: it survived 2353 runs of catching nothing,
because nobody could see that it caught nothing.

## 2026-09-08

Only the rows that MOVED are repeated here; every other row stands at its
2026-09-07 value and evidence.

| capability | score | evidence | re-measure |
|---|---|---|---|
| Mutation measurement (`mutants` stage) | 5 `[judgment]` | Replaces the receipt row, which is deleted along with the thing it scored: there is no document to sign, forge or judge. The merge gate now runs the measurement itself, in the foreground, on the merged tree — configuration read first so a retired key is refused before a suite runs, the measurement last so a red suite never pays for one — and refuses an unaccepted survivor by name, with the mutant on the first line. Every verdict writes one gate-log line carrying the counts, which is what makes the row scoreable at all: the receipt stage refused 150 merges over three weeks and logged no reason for 141 of them. Scored on judgment because it has measured **zero merges so far**; the number is a claim about the design answering the receipt's named defects, not about observed catches. It moves the day the command below has runs to count. | `aphrollo gate stats --since 7d`, the `mutants` row: green/red counts and the reason column (`survivor=`, `disk=`, `skipped:not-declared=`, `unmeasured:gremlins-windows=`) |
| Mutation receipts | — | Deleted. `gate receipt`, `gate mutants status`, `gate mutants watch`, `run --job` and `go` are unknown verbs; nothing signs, carries or checks a document. The "what to fix first" item this row carried on 2026-09-07 — make the receipt refuse to certify what it had already warned about — is closed by removing the receipt rather than by fixing it: the producer script that dropped `APHROLLO_MUTANTS_ARGS` is gone too, because the binary now builds the argv and runs the tool itself. | none; the row has no subject |

The layer this changes is the one 2026-09-07 called weakest. Certification is
gone as a category: there is no artifact between the measurement and the
judgement for a reader to trust and then stop checking. What replaces it is a
measurement whose refusal names the mutant, and whose every verdict is
counted — which is the shape the strongest rows on this table already have.

The fail-first RED proof is now the highest-value uninstrumented stage on the
board, unchanged from 2026-09-07 and no longer second in line behind the
receipt.
