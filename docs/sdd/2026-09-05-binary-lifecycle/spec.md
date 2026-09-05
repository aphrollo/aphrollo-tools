# Binary lifecycle: install never dirties the primary, update builds from origin/main, the session says when the binary is behind

Issues: #345, #340. Spec tree is scaffolding; it is deleted in the merge that lands the last lane.

## Problem

On the Windows box nothing deploys the binary. `/usr/local/bin/aphrollo` ships on merge through `deploy/deploy-prod.sh`; here the binary is whatever somebody last built by hand, and on 2026-09-05 `C:\Users\olive\bin` held 13 hand-renamed copies beside the live one.

Three defects chain into that:

1. `gate init` rewrites the managed CLAUDE.md block in the repo's primary checkout with the binary's newer template. The primary is merge-only, so nothing can commit the block there, and the primary shows `M CLAUDE.md` at every session start.
2. `workspace sync` refuses to fast-forward on any dirty tracked file (`internal/workspace/sync.go` lines 49-53), stricter than git, which refuses only when the update touches a dirty path. The primary therefore stayed at `e6e8039` while `origin/main` was at `ca47dba`.
3. `gate self-install` builds whatever tree `--repo` has (`internal/cli/selfinstall.go` lines 26-50, `-buildvcs=false`), so it built the stale primary; and the binary carries no revision (`go version -m` prints `(devel)`), so nothing can say it is behind.

## Decisions

- chose skipping the managed-block write in a merge-only primary and printing one notice over writing it, because the primary cannot commit and the dirt blocks sync and update downstream.
- chose letting `git merge --ff-only` decide over refusing on any dirty file, because git already refuses exactly the dangerous case and reports why.
- chose building `update` from `origin/main` in a detached temporary worktree over the cwd repo's tree, because the primary is routinely behind or dirty and a lane is never the trunk.
- chose stamping the commit with `-ldflags -X` into a leaf package over `-buildvcs`, because builds run from dirty worktrees where vcs stamping fails or embeds the wrong revision, and the tdd package must read the stamp without importing cli.
- chose a top-level `aphrollo update` verb over `gate update`, because the surface collapse (#342) puts box operations at top level and this verb should not move twice; `gate self-install` stays and shares the swap.
- chose a network check at session start, cached one hour with a two second budget and silent on any failure, over comparing against a local ref, because a local ref is only as fresh as the last fetch (user decision 2026-09-05).
- chose `git ls-remote` against the fixed aphrollo-tools remote URL over the cwd repo's remote, because the notice is about the binary, and the hook runs in every repo on the box.
- chose no behind-count in the notice over `rev-list --count`, because counting needs the remote objects locally and the two short shas already say what to do.

## Boundaries

- No change to the Linux deploy path or `deploy/deploy-prod.sh`.
- No automatic update; the notice tells, `update` does.
- No other verb moves or renames (that is #342, #346).
- `update` does not sweep the hand-made `.old`/`.prev` suffixes; only the `.stale-` copies self-install already owns.

## Acceptance criteria

1. Running `gate init` against a repo whose target is a merge-only primary (has a linked worktree, holds `main`) leaves its CLAUDE.md byte-identical and prints exactly one line naming the block as behind the template and a lane as the way to land it. Against a lane worktree of the same repo the block is written as today.
2. `workspace sync` on a primary whose only dirty file is untouched by the incoming commits fast-forwards; the dirty file's content is preserved; the line printed says how many commits it moved.
3. `workspace sync` on a primary whose dirty file IS touched by the incoming commits leaves HEAD where it was, exits 0, and prints git's own refusal text.
4. A binary built by `gate self-install` or `aphrollo update` answers `aphrollo version` with `aphrollo <sha7> built <RFC3339 UTC>`; one built with plain `go build` answers `aphrollo (unstamped)`.
5. `aphrollo update` fetches, and when the embedded commit equals `origin/main` prints `aphrollo update: already at <sha7> [skip]` and changes no file.
6. `aphrollo update` on a repo whose working tree is dirty and behind builds from a detached worktree at `origin/main`, never from the working tree, and the temporary worktree is removed even when the build fails.
7. After `aphrollo update` the installed binary is the freshly built one, the previous one is renamed aside with the `.stale-` prefix, and earlier `.stale-` copies nothing holds are gone, the same three lines self-install prints today.
8. At session start, a stamped binary whose commit differs from the remote `main` head adds one advisory line: `aphrollo binary is behind origin/main (built at <sha7>, origin at <sha7>): run aphrollo update`. An unstamped binary, a binary at head, a remote that does not answer within 2 s, and any error all add nothing.
9. Two session starts within one hour ask the remote once; the second reads the cached head.
