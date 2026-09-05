# Discard wall: the git shim refuses a command that destroys uncommitted or unmerged work, with the numbers and one override

Issue: #343. Depends on the verb-surface tree's B1 lane (the `gate allow <wall>` / `gate revoke <wall>` family). Spec tree is scaffolding; it is deleted in the merge that lands the last lane.

## Problem

Done work gets deleted by a reflexive `git reset --hard`, `git checkout -- .`, `git restore <path>`, `git clean -f`, `git stash drop` or `git branch -D`, typed by a session that meant to clean up one thing. Nothing stands in the way: the shim takes a lock for these and lets them run, and the primary wall refuses only moves off `main`. Git offers no pre-checkout or pre-reset hook; `post-checkout` fires after the work is gone. The shim already sees every typed `git`, so the wall is a classification there.

## Decisions

- chose the git shim over a git hook, because git has no hook that fires before a checkout, reset, restore or clean; the shim is the only point that sees the command before it runs.
- chose refuse-with-numbers over a confirmation prompt, because hooks and shims cannot ask a question; the refusal names what would be lost and the one way through, the shape every gate refusal already has.
- chose measuring the actual loss (modified files, insertions and deletions, untracked files, unmerged commits) over refusing the verb form outright, because a `reset --hard` on a clean tree destroys nothing and must pass silently; a wall that fires on clean trees is turned off within a week.
- chose a one-shot waiver (`gate allow discard` arms exactly one discarding command, expiring after 5 minutes) over a session switch, because the failure this wall exists for is "turned it off to reset one file, forgot, reset --hard an hour later" (user decision 2026-09-05).
- chose `APHROLLO_DISCARD=1` as the scripted override over none, because a script that must discard (a fixture reset) has no session to arm; every use is logged and counted in `gate stats` beside the primary waiver and the queue bypass.
- chose leaving the gate's own git calls out of scope by construction over an allow-list, because the tdd package resolves real git past the queue dir; only typed and agent-typed commands reach the shim.
- chose `push --force` and history rewrites as out of scope, because they destroy remote history, not local work, and the receipt and merge gates own that surface.
- chose `rm -rf` on tracked paths as out of scope here, because that is the Bash guardrail's policy table, not the git shim; it is filed separately when this lands.

## Boundaries

- No change to which commands are locked or queued; the wall runs before the lock is taken.
- No new git hook.
- `stash push` (reversible), `commit --amend` (reflog), `reset` without `--hard`/`--merge` (index only) pass untouched.
- Remote history (`push --force`, `push --delete`) and `rm -rf` are not this wall.

## Acceptance criteria

1. In a worktree with 3 modified tracked files (+212/-40) and 2 untracked files, `git reset --hard` through the shim exits 1 without running git and prints exactly: `gate: refused — reset --hard discards 3 file(s), +212/-40 uncommitted; aphrollo gate allow discard arms one command, APHROLLO_DISCARD=1 for scripts`. The tree is unchanged.
2. The same command on a clean tree passes to git with no extra output.
3. `git checkout -- <path>` and `git restore <path>` are measured over the named paths only: with `a.txt` modified and `b.txt` modified, `git restore b.txt` reports `discards 1 file(s)` and the diff numbers of `b.txt` alone; `git restore --staged b.txt` passes (index only).
4. `git clean -fd` with 2 untracked files reports `clean -fd discards 2 untracked file(s)`; `git clean -n` passes (dry run).
5. `git stash drop` and `git stash clear` with a stash present report `stash drop discards 1 stash entry(ies)`; with no stash they pass.
6. `git branch -D <b>` where `<b>` has 2 commits not on HEAD reports `branch -D <b> discards 2 unmerged commit(s)`; a fully merged branch passes; `git branch -d` passes (git refuses unmerged itself).
7. `git worktree remove --force <path>` where the tree has 1 modified file reports `worktree remove --force discards 1 file(s), +<a>/-<d> uncommitted in <path>`; a clean tree passes.
8. After `aphrollo gate allow discard`, the next discarding command in that session passes, the shim logs `override-discard-used`, and the following discarding command is refused again; `gate allow` lists `discard armed until <RFC3339> by <session>` while armed; 5 minutes later, unused, it is gone and the command is refused.
9. `APHROLLO_DISCARD=1 git reset --hard` passes and logs `override-discard-env`.
10. `aphrollo gate stats` counts both override rows under denies / overrides, and counts each refusal as `git-discard-refused:<form>`.
11. The gate's own `reset --hard` in the mutants worktree and `worktree remove --force` in prune still run without a refusal (they never enter the shim).
