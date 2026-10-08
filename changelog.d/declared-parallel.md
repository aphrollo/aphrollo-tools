level: minor

A repo can let its declared pre-commit and pre-merge commands run side by side, within a budget it sets.

### What you will notice

- `{ argv = [...], parallel = true }` marks a declared command. A command without the key is a barrier: it runs alone, in declared order, after everything before it has ended, so a generator still runs before the checks that read what it wrote.
- Consecutive parallel commands form a group. A command in a group starts only while the `weight` of those running (default 1, `weight = 2` on a heavy one) plus its own fits `parallel-budget` in `[aphrollo.precommit]`, which is the box's job count when unset. One heavier than the budget runs alone.
- Every command of a group runs to its end, so the refusal lists every red. Each command's lines are written whole, in declared order, as a run one after another would print them.
- The gate log records a `declared-parallel` event with `commands`, `wall_secs` and `sum_secs`, so the saving shows.
- With no `parallel` key nothing changes.
