level: minor

`aphrollo stats --ab` and the report's A/B section now count every lane and say when the comparison is decided.

### What you will notice

- A lane made by a plain `git worktree add` is in an arm: a lane that recorded none gets the arm its repo and name hash to, worked out when the log is read, so old lanes are counted too and no log is rewritten.
- A `<lane>-merge` branch (a premerge gate fired on it, or its lane has events) is the same lane, so its green ends the lane's time to green.
- A time to green of n 0 says whether no red occurred, a red has no green yet, or decisions were dropped for the budget and nothing was recorded. The readout says what the budget drops are: red-green decisions the hook did not finish in time.
- Instead of "30 lanes an arm", the readout gives three pre-registered metrics (escapes per lane, the primary; time to green p50 and denies per lane as guardrails): enforce minus warn with a 90% bootstrap interval from a fixed seed, and a verdict: deciding, decided: enforce better, decided: warn better, no meaningful difference, or max reached at 50 lanes an arm ("too small to measure, decide on friction and cost"). The language rows always print.
- `aphrollo report --issue` opens the `A/B ready: <repo>` issue when the primary metric is decided or an arm reaches 50 lanes, and names the verdict.
