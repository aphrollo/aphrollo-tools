level: patch

`run` no longer starts a command twice when the job object guard cannot be joined.

### What you will notice

- A Before hook that replaces the child's process attributes can no longer drop the suspended start the job guard relies on: it is put back after the hook runs, so the child never runs before it is inside its job.
- When the child may already have run past its start (a failed resume), the start fails with the guard's reason instead of falling back to a second start.
