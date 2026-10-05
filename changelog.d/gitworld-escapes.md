level: patch

A sibling lane's new branch no longer reads as a test leak, and a leak class opens one escape issue per repository.

### What you will notice

- The git-state canary around mutation runners no longer refuses a result, or records an escape, because another session made a lane on a branch not named `lane/*` (`feat/x`, `fix/y`): a branch a worktree has checked out at either reading is a lane's. The gate's own head-baseline checkouts and a fast-forward over commits already on origin are ordinary work too. A branch checked out nowhere, a worktree outside the lane dir and an unrecorded unpublished commit are still caught.
- A `gitworld:` escape is fingerprinted on the repository (its shared git directory, so every lane agrees), the stage and the parts that changed, never the ref names inside them: one class is one issue however many lanes and branches it comes through. Every automatic escape now keys on the repository rather than the lane checkout it was seen from, so a class already open under the old key may open once more.
- The canary no longer takes the gate's own head-baseline checkouts (the `.aphrollo-head-*` worktrees under the gate state's `head-wt` directory) for a test leak, in any tree: the creator and the canary read one constant. A merge the owner makes in a lane is recorded by the post-merge hook as the post-commit hook records a commit, and a move of the checked-out commit onto a commit that is on origin's trunk is the owner's too.
