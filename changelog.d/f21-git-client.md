level: minor

The workspace verbs and the pre-merge steps now read what sits in git's files from those files, and no longer assume a branch is called main.

### What you will notice

- `workspace commit` starts 5 git children where it started 8, and `workspace merge` 4 where it started 9: HEAD, the branch, the worktree list, a ref and a remote's URL are read from the repository's files by one client per worktree.
- A repo whose trunk cannot be told (no `origin/HEAD`, and no `main` or `master`) is refused by the verbs that need a base, with the fix: `git remote set-head origin --auto`, or `--base`. They used to take `main`.
- The primary-checkout rule and `gate doctor` hold the checkout to the repo's trunk, whatever it is called.
- `workspace merge` and `--wait` say "no check ran (all N skipped)" when every check on the head concluded skipped, instead of saying every check passed. Skipped checks never made a green verdict for the gate to reuse, and still do not.
- During a merge in progress the staged set is read again when trunk moves to hold the incoming tip.
