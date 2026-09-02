# Gremlins mutation-testing pilot (aphrollo-tools)

Ticket `01a0502d-d925-25424941a8ce`. **Measurement only** — nothing here is wired
into CI, the reviewer role policy, or the `aphrollo` CLI. This document records
what a diff-only gremlins run costs and whether it earns a place in the
pre-merge reviewer pass.

## Why

The `aphrollo tdd` fail-first gate proves a test went RED once, at its commit.
Nothing ever re-proves a test still constrains the code it guards. Mutation
testing closes that: it perturbs source, re-runs the tests, and a mutant that
*survives* is a line no test actually pins. This pilot measures the cost of that
signal before it touches reviewer latency.

## Tool & method

- **Tool:** gremlins `v0.6.0` (`github.com/go-gremlins/gremlins`), installed with
  `go install ...@v0.6.0` under go1.26.7.
- **Mode:** diff-only (`unleash --diff <base>`), which restricts mutation to the
  lines a commit changed (merge-base -> working tree). A whole-module run was
  never used — it is the wrong shape for a pre-merge gate.
- **How representativeness was obtained:** the ticket branch has no code changes,
  so a `--diff origin/main` run would mutate zero lines. Instead each run was
  taken at a real recent commit with `--diff <commit>^`, so the mutated set is
  exactly that PR's touched lines — the same shape a reviewer pass would see.
- Runs were taken in throwaway detached worktrees; the ticket branch itself
  carries only this document.

### One operational fact that shapes everything below

gremlins gathers coverage by running the **whole module's** test suite before it
mutates anything — even in `--diff` mode for a one-line change. Two consequences:

1. **Any** failing/flaky/env-gated test in **any** package aborts the entire run
   with `failed to gather coverage`. In this repo `internal/refactor`'s
   rust-analyzer e2e test fails in the sandbox (rust-analyzer is on PATH so it
   does not self-skip; it returns `No references found at position`). That single
   unrelated failure blocked the whole pilot until the file was moved aside in
   the measurement worktree. A green whole module is a hard precondition.
2. The whole-module coverage gather is a **fixed time floor** paid on every run,
   independent of diff size. On a cold Go test cache — which is the real state of
   a fresh PR checkout — that floor is the dominant cost for small diffs.

## Numbers

Four diff-only runs. "Skipped" = mutants outside the diff (the rest of the
module); it confirms diff scoping is working, and is not a cost.

### aphrollo-tools

Module: 75 source files, 95 test files. Full unit suite ~17.8s wall.

```
commit    touched src   killed  lived  not-covered  not-viable   mutation-exec   total wall
5da2368   cli.go (+27)     1       0        2            0           0.99s          10.9s   (warm-ish cache)
490be6f   runner.go +      8       2        0            0          18.9s           37.8s   (cold cache)
          precommit.go
          (+82 src)
```

- `5da2368`: efficacy 100% (1/1 covered killed), 2 survivors were **not covered**
  at all.
- `490be6f`: efficacy 80% (8/10 covered mutants killed), mutator coverage 100%.

### aphrollo-api (scale probe)

Module: 388 source files, 456 test files, **106 integration-tagged test files**.
Cold unit suite ~22s wall.

```
commit    touched src           killed  lived  not-covered  not-viable  mutation-exec  total wall
3c527d7   config.go (+11)          6       0        0            0          1.5s          23.0s   (cold)
4ea0d12   store/customers.go       0       0        0            0          0.67s         (cold)
          (+41/-35)                --- EMPTY REPORT: 0 mutants, efficacy 0%, coverage 0% ---
```

### Per-touched-line cost

The honest framing is **not** a linear per-line rate — cost is a large fixed
coverage floor plus a small per-mutant term:

- **Fixed floor** (whole-module coverage gather, cold): ~10s (tools) / ~22s (api).
  Paid every run. This is what a reviewer waits for on a tiny diff.
- **Per-mutant execution:** observed 0.25s/mutant (api `config`, fast package) to
  1.9s/mutant (tools `tdd`, slow ~15s package). It tracks the affected package's
  test-suite runtime, since each mutant re-runs that package's tests.
- Net: `490be6f` (82 lines, 10 mutants) = 37.8s cold ≈ 0.46s/touched-line, but
  that number is meaningless in isolation — 90% of it is the fixed floor + the
  slow `tdd` package, not the line count.

## Survivors: equivalent vs. real gap (the whole decision)

Four survivors across the measured commits. **Every one is a real untested path;
zero are genuinely-equivalent mutants.**

- `5da2368` cli.go:515 & :529 — `CONDITIONALS_NEGATION` on the `case cerr != nil`
  / `case gerr != nil` **error branches** of the cargo/git shim installer. Both
  **not covered**: no test exercises the shim-install-failure warning path. Real
  gap.
- `490be6f` runner.go:495 & :500 — `CONDITIONALS_BOUNDARY` (`< 0` -> `<= 0`) in
  `quotedWords`. `:495` survives when a string *starts* with `"` (open index 0);
  `:500` survives on an empty quoted string `""` (close index 0). `quotedWords`
  has no direct test, so both edge cases are unexercised. Both are killable with
  one-line inputs (`"x"` and `a""b`). Real gaps, not equivalent.

Ratio: **4/4 real, 0/4 equivalent.** For this codebase gremlins produced no
equivalent-mutant noise — the survivors were all worth acting on. That is the
single most important result here: the signal was clean.

## Is aphrollo-api (the biggest module) blocked?

Runtime is **not** the blocker for a small, unit-tested diff: the `config` commit
ran clean in 23s cold with a perfect 6/6 kill. gremlins' documented slowness on
large modules did not bite, because `--diff` keeps the mutant count tiny
regardless of module size — only the fixed ~22s coverage floor scales with the
module, and 22s is acceptable for a pre-merge pass.

The real blockers for api are two, and they are structural, not speed:

1. **The persistence layer is invisible.** 87 of api's 102 `store/postgres` test
   files are `//go:build integration` (Postgres-backed) and are skipped by
   gremlins' default (unit-only) coverage gather. The `customers.go` store commit
   — a 76-line rewrite of real persistence logic — produced a **completely empty
   mutation report**: 0 mutants, efficacy 0%, coverage 0%. That is worse than no
   tool: a PR that rewrote persistence code gets a silent all-clear. Covering it
   needs `--tags integration --integration`, which (a) requires a live Postgres
   in the reviewer runner and (b) makes `-i` re-run the **entire** ~22s suite per
   mutant — for a 10-mutant diff that is minutes, and this is exactly the
   large-module slowness gremlins warns about.
2. **The whole-module-green precondition** (proven above with the rust-analyzer
   flake) is harder to hold on api, which has more surface and DB-dependent tests.
   The unit-only suite is green without a DB (verified: `go test ./...` exit 0),
   so gremlins *can* run — but only over the unit-covered subset, which returns us
   to blocker 1.

**0.x / no backcompat:** gremlins is pre-1.0 with no compatibility guarantee. The
`--diff` flag, the JSON output schema (`-o`), and the mutator set can change
between minor releases. Any future wiring must pin the exact version and treat
the JSON schema as unstable.

## Recommendation: **conditional go — as an opt-in, not a gate (for now)**

- **For aphrollo-tools and other unit-test-first modules:** the signal is real
  and clean (4/4 survivors actionable, 0 equivalent noise) and the cost is
  tolerable (~10-40s per diff). Worth offering as a **manual / opt-in reviewer
  step**, run diff-only.
- **Do NOT make it a blocking pre-merge gate yet**, for three reasons:
  1. The whole-module-green precondition means one unrelated flaky test (the rust
     e2e here) turns every mutation run red — a gate that fails for reasons
     orthogonal to the diff will be routed around, not fixed.
  2. On aphrollo-api, the biggest and most important module, the default run is
     blind to the integration-tested persistence layer and silently returns an
     empty report — a gate that green-lights untested persistence changes is
     actively misleading.
  3. 0.x with no backcompat is not a foundation for a required gate.
- **Path to a real gate later:** (a) get the whole module reliably green in the
  runner env (fix or properly skip-guard the rust e2e); (b) decide the
  integration-coverage story for api (either accept unit-only mutation scope and
  document that the store layer is out of mutation scope, or stand up a
  Postgres-backed `-i` run on a slower cadence than per-PR); (c) pin the gremlins
  version and snapshot its JSON schema.

## What was NOT measured, and why

- **A whole-module gremlins run.** Out of scope — the pre-merge shape is
  diff-only, and a full run on a large module is the documented slow path we are
  explicitly avoiding.
- **api integration-mode mutation** (`--tags integration --integration`). Not
  run: it needs a live Postgres wired into the measurement env and re-runs the
  full suite per mutant. Its cost is bounded above by (mutant count x ~22s
  suite), which is already enough to rule it out for per-PR use; a precise number
  was not worth the setup for a no-go input.
- **Other modules** (aphrollo-web is not Go; askdoc/docflow/fetch/lens not
  probed). The two Go modules measured (tools = small, api = largest) bracket the
  range.
- **Timeout / not-viable behavior under load.** Not-viable was 0 across all runs;
  no infinite-loop mutant surfaced in these diffs, so the timeout coefficient was
  never exercised.
- **CI wall-clock on the actual runner.** All numbers are from the dev box; a
  cold runner with no Go build cache would pay a one-time compile cost on top.
```
Reproduce: go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
           gremlins unleash --diff <commit>^   # inside a green module worktree
```
