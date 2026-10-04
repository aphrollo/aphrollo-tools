level: patch

The gate reads its own history from the per-repo event log instead of the shared gate.log text file.

### What you will notice

- `gate stats`, the weekly digest, `gate gc --known`, the build-budget floor, the status line, the whole-suite rerun refusal and the pre-commit marker give the same answers, now computed from the events of the repository they concern.
- A stage event now carries its command and root, and a deny or override event carries the file or session it concerned, never the command text.
- A window that opened inside the current month no longer skipped that month's event file.
- The older gate.log keeps being written only for the pre-merge readers that still use it.
