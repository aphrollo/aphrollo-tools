level: patch

A declared command that the merge gate runs instead of reusing now says why, and the merge gate's stage lines carry the same time stamp as the rest of `workspace merge --wait`.

### What you will notice

- For each declared command with `inputs` the merge gate prints `[run] <cmd>: no reuse — <reason>` when it runs it: no recorded verdict for this tree, the recorded verdict was red, a glob matched no file, a glob selects an ignored file, a glob leaves the root, the tool could not be found or read, the store could not be read, or `baseline commands never reuse`. When an earlier run of the same command and root is on record, the reason names every part that moved since it: `inputs and lockfile/manifest changed since the last recorded run (3m ago)`.
- The store keeps the hash of each part with each entry, and a write sequence so two entries made in one second keep their order. There is no schema bump: entries from before read as no record, and an older binary that rewrites the store drops the new fields, which reads the same way. That is harmless.
- When the commit gate cannot key such a command (for example a glob that matches no file), it prints `[note] <cmd>: not recorded for reuse — <reason>` instead of staying silent.
- A tool named by a path inside the repo (`./scripts/lint.sh`) is keyed by its content, found from the root, so the lane's checkout and the merge gate's checkout agree on it.
- A `git merge` of main into a lane is already judged: the `pre-merge-commit` hook runs `gate premerge`, which records declared verdicts the way the commit gate does. Nothing was added to the installed hook set. The merge gate also records its own real runs, so the next merge over the same inputs reuses them. A root with no changed file is not judged at the commit, and the merge's record covers it.
- Every line `gate premerge` prints during `workspace merge --wait` starts with the same UTC `HH:MM:SS` stamp as the outer lines.
