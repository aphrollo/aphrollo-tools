level: minor

`workspace merge` now uses GitHub's merge queue when the PR's base branch has one.

### What you will notice

- A direct merge GitHub would refuse becomes an enqueue bound to the judged head, with one line saying where the PR stands in the queue.
- A CI verdict for an older base no longer refuses the merge under a queue: the queue tests the current merge itself. The lane, text, the PR's own checks and the merged tree's laws are still judged first.
- `--wait` waits until the queue has merged the PR, or exits 1 naming the failing merge_group job when the queue removed it; `--wait <pr>...` enqueues every PR before waiting.
- `workspace pr` takes `--body-file` and refuses when the file cannot be read, instead of opening the PR with an empty body.
- A repo with no merge queue merges exactly as before.
