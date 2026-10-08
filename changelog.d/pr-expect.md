level: minor

A PR body can now say which metric its change should move, and `aphrollo report` checks whether it did.

### What you will notice

- A line `expect: <metric> <p50|p90|rate> <down|up>` in the PR body (for example `expect: merge-queue p50 down`) is checked when `workspace pr` or `workspace submit` opens the PR: an unknown metric or a malformed line is refused, and the message names the valid metrics.
- `workspace merge` records the PR's expect lines on its merge event, with the newest release tag at that moment.
- `aphrollo report` has an Expectations section. A PR whose release has 30 lanes of newer events shows before and after values and the same Mann-Whitney verdict as the speed section (`~` is no clear change; a sample that is too small says so), and whether the expectation held. One that did not hold is listed as a proposal. Fewer than 30 lanes reads `pending (n of 30 lanes)`.
