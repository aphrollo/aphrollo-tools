level: minor

The gate reads its own history from the per-repo event log instead of the shared gate.log text file.

### What you will notice

- `gate stats`, the weekly digest, `gate gc --known`, the build-budget floor, the status line, the whole-suite rerun refusal and the pre-commit marker give the same answers, now computed from the events of the repository they concern. History from before the upgrade, whose events carry no command or root, is read once from gate.log, so nothing starts from zero.
- `gate stats --since <window>` prints `events since <date>` first when the window opens before the oldest line kept. With no history at all it says so and exits 1, and it refuses an event log written by a newer binary.
- A stage event now carries its command and root when the stage is one the gate itself ran. A deny or override event carries the file or session it concerned. Text typed in a shell command never goes on an event.
- A window that opened inside the current month no longer skipped that month's event file.
- gate.log keeps being written only for the pre-merge readers that still use it.
