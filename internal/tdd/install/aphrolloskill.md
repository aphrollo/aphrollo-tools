---
name: aphrollo
description: aphrollo per-session switch — /aphrollo off|on|status
argument-hint: "[off|on|status]"
---

<!-- Written by `aphrollo install`; edit the template in aphrollo. -->

`/aphrollo <arg>` is handled by the hook `aphrollo gate userpromptsubmit`. Reading this after typing
it means the hook is missing: say so and stop.

- `/aphrollo off`: every aphrollo session hook goes silent and decides nothing for this session
  (edit-time guidance and denies, test runs, gate lines, turn-end checks, injected context, reply
  style). The git-side gates (commit, merge, push) stay on.
- `/aphrollo on`: back to normal.
- `/aphrollo status`: one line, on or off and what stays on.

`/tdd off|on|status` is the same switch under its older name. `TRELLIS_OFF=1` switches a whole
process.
