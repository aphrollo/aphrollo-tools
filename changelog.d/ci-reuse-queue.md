level: minor

A pull request in a repo with a GitHub merge queue is no longer tested twice when nothing changed between its own run and the queue's run.

### What you will notice

- `aphrollo ci reuse` is a new verb for any repo's workflow. Run in the changes job, it answers `reuse=true` or `reuse=false` (the reason goes to stderr) and the heavy jobs read it. On a push to main it reuses the merge queue's run or the merged pull request's run. With `-event merge_group` it reuses the run of the one pull request in the queue group when the group's tree is the tree that run tested. A group of several pull requests, a re-run, a red run, a pull request that moved after it was queued, or a lookup that fails always runs everything.
- This repo's pipeline now uses it on the merge queue: a pull request based on the current main runs the suites once. The Windows shards still start on the queue and their steps stand down, because the queue requires the shard checks by name.
- The README has a workflow to paste into your repo. `aphrollo check` warns when a workflow listens on `merge_group` and never calls `aphrollo ci reuse`.
