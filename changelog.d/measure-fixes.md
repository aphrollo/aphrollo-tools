level: patch

`aphrollo stats` now counts first-run CI reds, merge-queue reds, queue-merged lanes and overrides the way they happened.

### What you will notice

- A PR whose first run failed no longer shows as green: `workspace merge` reads the PR's run list and records the first head's conclusion (cause from the failed job) at the run's own time, so it comes ahead of the green of the fix pushed after it.
- A PR the merge queue removes for failed checks records a red `queue` result, and a lane whose PR run was green but whose queue run failed counts as red by cause `queue`.
- A PR queued by `workspace merge` and merged by GitHub later is recorded as merged on its lane when local trunk takes the merge in, instead of being left open in the speed measure.
- An allowed narrowed rerun (`override-bash-narrowed`) is no longer an override, and one deny is at most one wrong block.
- A PR queued without `--wait` and dropped by the queue for failed checks is recorded red by the next `workspace sync`.
- A sync that records PRs the merge queue landed names them as queue-landed merges of `workspace merge`, apart from merges made outside it.
- The first run of a PR is its first attempt: a failure fixed with a rerun still counts as a red first run, and a PR the queue dropped for failed checks keeps its queue red after being queued again by hand.
