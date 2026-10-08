level: minor

A repo can let its declared pre-commit and pre-merge commands run side by side within a budget it sets, and a command judged against HEAD is skipped when its tree equals HEAD's.

### What you will notice

- `{ argv = [...], parallel = true }` marks a declared command. A command without the key is a barrier: it runs alone, in declared order, after everything before it has ended, so a generator still runs before the checks that read what it wrote.
- Consecutive parallel commands form a group. A command in a group starts only while the `weight` of those running (default 1, `weight = 2` on a heavy one) plus its own fits `parallel-budget` in `[aphrollo.precommit]`. Unset, the budget is what the box carries at about 8 threads and 8 GB free per command, at least 1, as for a race run. One heavier than the budget runs alone.
- Every command of a group runs to its end, so the refusal lists every red, in declared order. Each command's block is written whole as it finishes, under `[<cmd>] (finished n of m)`, and a still-running notice is written at once.
- Two commands judged against HEAD never add or remove the HEAD worktree at the same time.
- The gate log records a `declared-parallel` event with `commands`, `wall_secs` and `sum_secs`; `aphrollo report` adds `sum_secs - wall_secs` in a "declared parallel saved" row.
- A command with `baseline = "lines"` and `inputs` is skipped, printing `[reuse] <cmd>: inputs equal the base's`, when no input, lockfile or tool differs from HEAD's tree (read from git, no checkout). It runs otherwise and on any doubt, and the merge says what differs.
- With no `parallel` key nothing changes in how commands run.
