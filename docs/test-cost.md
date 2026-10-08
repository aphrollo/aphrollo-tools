# Test cost

A slow suite is a tax on every merge, and it grows one test at a time. aphrollo
makes the cost visible and ratchets it, the way it does for module size: no gate
wall, a number that may only fall.

## What is recorded

The merge gate already runs the suite on the merged tree. From the output that run
prints, with no extra flag and no extra run, it keeps one `suite.cost` event per
merge in the repo's event log: the suite's seconds, and the seconds of its slowest
tests and packages (those of a second or more, at most 50 of each).

| runner | recorded |
| --- | --- |
| go | each top-level test and each package (`go test -json`) |
| cargo with nextest | each test |
| cargo test | each test binary and doctest run |
| vitest | each file (the default reporter states no per-test time) |
| pytest and others | the suite's seconds only |

## Where it shows

- `aphrollo stats` and the weekly `aphrollo report` print merge time by stage (the
  local gate, CI) and the ten slowest tests with their trend.
- When a commit adds a test whose recorded time is over 10s, the commit gate prints
  one `[info]` line naming it. It never blocks.
- The `test_cost` law, from the opt-in `cost` preset
  (`aphrollo ratchet init --preset cost`), holds two figures: the median suite
  seconds, and the median count of tests at or over `threshold_secs`, each over the
  newest `window` recorded merges. It judges nothing until `min_runs` merges are
  recorded, a median does not move for one slow run, and `tolerance_pct` absorbs
  the rest. A baseline only goes down; `aphrollo ratchet check --adopt test_cost` is the one
  way to raise it. The figures come from this machine's record, so a baseline
  adopted on a much faster or slower box needs a wider tolerance.

## What makes a test expensive

- **A real git repository or subprocess per test.** Build the repository once for
  the package and copy it, or fake the process.
- **A sleep or a poll.** Wait on the event with an injected clock or a blocking
  fake, never on the wall clock.
- **A binary built per test.** Build it once in `TestMain`.
- **A fixture rebuilt per test.** Build it once and make each test read it, or give
  each test its own cheap copy.
- **A large input where a small one proves the same.** Shrink it until the test
  still fails when the code is wrong.
- **A test that proves what a faster test already proves.** Delete it.

A test that is slow only under load, or passes alone and fails beside others, is a
broken test, not a slow one: fix its clock or its shared state.
