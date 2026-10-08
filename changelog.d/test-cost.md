level: minor

What the suite costs is now recorded at merge, shown, and can be ratcheted in any repo, so a suite cannot drift slow unseen. It adds no gate wall and no runner flag.

### What you will notice

- The merge gate writes one `suite.cost` event per merge it runs the suite for: the suite's seconds and its slowest tests and packages, read from the output the run already prints (`go test -json`, vitest's per-file lines, cargo's per-binary and nextest's per-test lines). A runner that states no timing is recorded by its total seconds only.
- A commit that adds a test whose recorded or first-run time is 10s or more gets one `[info]` line naming it, its seconds, the usual causes and `docs/test-cost.md`. It never blocks, and says nothing when no time is known.
- `aphrollo report` and `aphrollo stats` print a test cost section: merge time by stage (local commit gate, local merge gate, CI, queue wait; local stages only, labelled, when the log has no CI or queue time) and the ten slowest tests with their change since the run before.
- A new opt-in `test_cost` law, from `aphrollo ratchet init --preset cost`, holds the median suite seconds and the median count of tests over `threshold_secs`, over the newest `window` merges, to a baseline that only goes down (`--adopt` raises it). A median and `tolerance_pct` keep one slow run from failing it, and a repo with fewer than `min_runs` recorded merges is not judged.
- `docs/test-cost.md` says what makes a test expensive and how to fix it.
