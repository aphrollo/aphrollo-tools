# Verb surface: install, check and issue at top level; gate keeps hooks and ledgers; walls are waived by one family

Issues: #342, #346. Depends on the binary-lifecycle tree landing first (it adds the top-level `update` and `version` verbs this spec leaves alone). Spec tree is scaffolding; it is deleted in the merge that lands the last lane.

## Problem

`aphrollo gate` mixes three kinds of verb: hook entry points nobody should type (`sessionstart` ... `prepush`), box operations (`init`, `install`, `self-install`, `gc`, `doctor`) and repo judgement that also lives elsewhere (`ratchet check`, `docs check`, `sqlc check`). Under `gate`, `issue` reads as gate bookkeeping and gets skipped for a markdown follow-up, which the repo's own rule forbids. `premergecommit` carries the `+commit` only because it mirrors git's hook file name. The one waiver, `primary-edits on|off`, is named after its mechanism, and the discard wall (#343) is about to need a second waiver with a different scope.

`aphrollo workspace` has the loop right and leaks at the edges: `update` rebases a lane and now collides with the top-level `update` that rebuilds the binary; three verbs delete a worktree; `prune` skips every detached tree with no PR forever (five such trees on this box from 2026-09-03/04, one with 160 dirty files); `sync` demands the repo argument its siblings default; `verify` duplicates the commit gate on Go and Rust repos; and CLAUDE.md's command list names `prepare`, `cleanup`, `pr`, `ship`, none of which exist.

## Decisions

- chose `aphrollo install` absorbing `gate init` and `gate install` over keeping both, because a box has one setup step and two verbs with overlapping writes is how one of them is forgotten.
- chose `aphrollo check` as the read-only umbrella over the guards (`ratchet check`, `docs check`, `sqlc check`, `gate doctor`, and the app trio `verify` runs when a repo declares one) over a new stage, because it is exactly precommit's cheap stages without build or tests and gives a session one word for "judge the tree". `ratchet check --adopt` stays under `ratchet` because it mutates a baseline.
- chose `aphrollo issue` at top level over `gate issue`, because opening an issue is the repo's open-point verb, not a gate ledger.
- chose keeping `gate feedback` over folding it into `issue`, because it files against the tool's tracker with the reporting repo and tip attached, which is gate-specific (user decision 2026-09-05).
- chose `gate premerge` over `premergecommit`, because git's hook file keeps git's name and the verb should read like `precommit` and `prepush`.
- chose one waiver family `gate allow <wall>` / `gate revoke <wall>` over per-wall verbs, because every wall's refusal, stats line and doc then read the same, and the discard wall (#343) joins it without a new verb. `allow primary` is session-scoped (many edits), `allow discard` will be one-shot (one command); the scope is a property of the wall, not of the verb.
- chose silent aliases for one release for every old spelling (`gate init`, `gate install`, `gate issue`, `gate premergecommit`, `gate primary-edits`, `/tdd primary-edits`, `workspace update`, `workspace verify`, `workspace prune <repo> <branch>`) over a hard cut, because hooks, scripts and other repos' CLAUDE.md blocks still spell them; the `tdd` alias set the pattern.
- chose `workspace rebase` over keeping `workspace update`, because it is a rebase and the name is taken.
- chose two deletion verbs, `prune` (sweep) and `remove` (one, `--keep-branch` for the per-ticket case), over three, because the per-ticket `prune` form was `remove` without the branch delete.
- chose `list` printing age, dirty count and PR state per tree, and `prune --stale <age>` for detached trees with no PR and no lane marker, over leaving them, because a tree nobody can see is a tree nobody removes. `--stale` always prints what it would remove and removes only with `--apply`... no: `workspace` verbs execute by default, so `--stale` removes and `--dry` previews, matching every sibling.
- chose a test that reads the repo's CLAUDE.md command surface against the dispatch table over regenerating the section, because the section is prose and a test is what keeps prose honest.
- chose `gate self-install` staying as is over aliasing it to `update`, because it builds the working tree and `update` builds `origin/main`; they answer different questions.

## Boundaries

- No change to what any hook does, only to what it is called.
- No change to `update`, `version` (binary-lifecycle tree) or to `dev`, `refactor`, `find`, `outline`, `show`, `ratchet`, `sqlc`, `docs` as sub-trees.
- The discard wall itself is #343, not here; this spec only gives it the `allow` family to join.
- `claim`/`unclaim` stay under `workspace`.

## Acceptance criteria

1. `aphrollo install --repo <r>` performs today's `gate init` and `gate install` writes in one run and prints each step; `aphrollo gate init` and `aphrollo gate install` do the same and are listed in usage as aliases retiring next release.
2. `aphrollo check` in a repo runs ratchet check (no tighten), docs check, sqlc check when a sqlc config exists, doctor, and the app trio when the workspace declares one; exit 1 on any miss; prints one line per guard naming clean or the miss count; a repo with none of the optional guards prints `[skip]` for each.
3. `aphrollo issue "<title>" --label <l>` behaves exactly as `gate issue` today; `gate issue` still works.
4. `git`'s `pre-merge-commit` hook file installed by `install` invokes `aphrollo gate premerge`; `aphrollo gate premergecommit` still works; every message the routine prints starts `gate premerge:`.
5. `aphrollo gate allow primary` waives the merge-only rule for the session and prints the waiver line naming `aphrollo gate revoke primary`; `gate revoke primary` restores it; `gate allow` alone lists the active waivers as `<wall> since <RFC3339> by <session>` or `no waivers`; `gate primary-edits on|off` and `/tdd primary-edits on|off` still work; the refusal text names `aphrollo gate allow primary`.
6. `aphrollo workspace rebase` does what `update` did; `workspace update` still works.
7. `aphrollo workspace remove <repo> <branch> --keep-branch` removes the tree and leaves the branch; `workspace prune <repo> <branch>` still works and does the same.
8. `aphrollo workspace list <repo>` prints one line per tree: path, branch or `detached`, `<n>d` since its last commit, `<n> dirty`, and `PR merged|open|draft|none`.
9. `aphrollo workspace prune <repo> --stale 3d` removes a detached tree with no PR and no lane marker whose last commit and newest file are both older than 3 days, and skips one that is younger or dirty; `--dry` lists them instead.
10. `aphrollo workspace sync` with no argument syncs the cwd's repo.
11. `aphrollo workspace verify` prints a one-line deprecation pointing at `aphrollo check` and runs check.
12. A test in this repo fails when CLAUDE.md's command surface names a verb the dispatch tables do not have, or omits a top-level verb they do; the section is corrected so the test passes.
