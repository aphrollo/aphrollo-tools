level: patch

A declared command that the merge gate runs instead of reusing now says why, and the merge gate's stage lines carry the same time stamp as the rest of `workspace merge --wait`.

### What you will notice

- For each declared command with `inputs` the merge gate prints `[run] <cmd>: no reuse — <reason>` when it runs it: no recorded verdict for this tree, the recorded verdict was red, inputs changed, lockfile/manifest changed, tool changed, a glob matched no file, a glob selects an ignored file, a glob leaves the root, the tool could not be found, or the store could not be read. The store now keeps the hash of each part, so the reason names the part that moved; entries written by an older binary read as no record.
- When the commit gate cannot key such a command (for example a glob that matches no file), it prints `[note] <cmd>: not recorded for reuse — <reason>` instead of staying silent.
- A tool named by a path inside the repo (`./scripts/lint.sh`) is keyed by its content, found from the root, so the lane's checkout and the merge gate's checkout agree on it.
- The merge gate records its own real runs, so the next merge over the same inputs reuses them. A root with no changed file is not judged at the commit, and the merge's record covers it.
- Every line `gate premerge` prints during `workspace merge --wait` starts with the same UTC `HH:MM:SS` stamp as the outer lines.
