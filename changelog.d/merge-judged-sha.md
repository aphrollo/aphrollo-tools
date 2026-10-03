level: patch

`aphrollo workspace merge` now judges and merges the same commit: the PR head GitHub reports, read once.

### What you will notice

- A lane whose HEAD is not the PR head (commits not pushed, commits the lane lacks, or a diverged history), or whose worktree has uncommitted changes, is refused before anything is judged. The merge exits 2 with one line saying how the lane differs and what to run. Nothing is pushed for you.
- The gate, local CI and the CI verdict reuse are all built from the PR head, never from the lane's checked-out HEAD, on a plain merge, with `--wait`, and in a `merge --wait <pr>...` queue.
- The merge call carries that head to GitHub. If someone pushes between the judgement and the merge, GitHub refuses it and the merge exits 2 asking you to merge again, so a tree that was never judged cannot land.
- With `--wait`, a lane that still differs from the PR head after three polls is refused at once instead of waiting out the timeout.
