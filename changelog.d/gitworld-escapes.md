level: patch

A sibling lane's new branch no longer reads as a test leak, and a leak class opens one escape issue per repository.

### What you will notice

- The git-state canary around mutation runners no longer refuses a result, or records an escape, because another session made a lane on a branch not named `lane/*` (`feat/x`, `fix/y`): a branch a worktree has checked out at either reading is a lane's. A branch checked out nowhere, a worktree outside the lane dir and an unrecorded unpublished commit are still caught.
- The canary takes the gate's own head-baseline checkouts (the `.aphrollo-head-*` worktrees under the gate state's `head-wt` directory) for ordinary work in any tree; the creator and the canary read one constant.
- The checked-out commit moving is the owner's when it moves forward onto commits that are on origin's trunk (a pull or a fast-forward), over merge commits the post-merge hook recorded, or by a rebase or an amend whose new commits the new `post-rewrite` hook recorded after the run began. A move back or sideways onto a published commit, a move onto a sibling lane's tip, and a rewrite nothing recorded are still caught. The hook is `gate postrewrite`, written by `aphrollo install` and `gate init`; re-run `aphrollo install` to get it.
- A `gitworld:` escape is fingerprinted on the repository (its shared git directory, or for a pruned lane its place under `.worktrees/<repo>/`), the stage, and the set of changed part labels (the branches, the worktree registrations, the checked-out commit), never the ref names inside them. A class is that set of labels: different branches, lanes and paths changed within one part share one issue, and a change to another part is another issue.
- Every automatic escape keys on the repository rather than the lane checkout it was seen from. A class already open under the old key still stands for itself, so nothing opens twice on upgrade.
