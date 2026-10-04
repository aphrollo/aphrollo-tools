level: patch

The gate no longer writes `gate.log`: every record of what the gate did is the event log, and the merge gate's own readers (the last commit-gate verdict, a stored local-CI green for a tree, the retrospective's lane scan) read it too. The remaining direct `gh` calls (the escape verbs, the session-start issue line, the mutation gate's CI check and runner-report fetch) go through the host port, in the same sealed environment, with the same timeouts and no prompt.

### What you will notice

- A `gate.log` left from an earlier release is still read for history older than the event records, until 2026-11-05; nothing new is appended to it.
- An override or a refusal records its verdict, never the text that was typed with it (a reason, a command), because the event keeps typed text off the log.
- A local-CI green recorded before this release is not reused for the same tree once; the first merge after the upgrade runs the workflow again.
