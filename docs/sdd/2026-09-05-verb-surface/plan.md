# Plan: verb surface

Four lanes, all independent of each other in files; run B1, B2, B3 in parallel, B4 after B2 and B3 merge because its test reads the final dispatch tables. Every lane branches from `origin/main` after the binary-lifecycle tree has merged, commits one mixed test+impl commit under the tdd skill, and updates the README section it changes in the same commit.

Repo-wide facts every builder needs: `cargo` and `git` resolve to the queue shim; the hooks run the tests and print one `gate:` line per edit; the primary checkout is merge-only, work only in the lane worktree you are handed. Alias pattern: see `case "gate", "tdd":` in `internal/cli/cli.go` (line 92) and the README section "The `tdd` → `gate` rename" (line 1948) for how a silent alias is wired and documented.

## B1: premerge, and the allow/revoke waiver family (#342, part 1)

Files:
- modify `internal/cli/cli.go` lines 480-510: accept `premerge` beside `premergecommit` (same branch); the routine's own lines print `gate premerge:`. Add `allow` and `revoke` gate verbs dispatching to new `runGateAllow(args, stdout, stderr) int` / `runGateRevoke(...)` in a new file `internal/cli/allow.go`; keep `primary-edits` (line 399) dispatching to the same code as `allow primary` / `revoke primary`.
- modify `internal/tdd/install.go` line 41: `{"pre-merge-commit", "premerge"}`.
- modify `internal/tdd/receipt.go` line 469 and every other `gate premergecommit:` literal (grep) to `gate premerge:`.
- modify `internal/tdd/primaryedits_cli.go` lines 23-43: messages name `aphrollo gate revoke primary` / `aphrollo gate allow primary`; add `ListWaivers() []Waiver` reading the session's waiver state (`Waiver{Wall, Since time.Time, Session string}`), and generalise the state file so a wall name is part of the key (today only `primary`).
- modify `internal/tdd/primary.go` lines 27-29 comment and the refusal text `PrimaryMergeOnlyReason` to name `aphrollo gate allow primary`.
- modify `internal/tdd/session.go` lines 108-120: `/tdd allow primary` and `/tdd revoke primary` beside `primary-edits on|off`; the "valid:" list names both.
- modify `internal/tdd/claudemd.go`: the template line naming `aphrollo gate primary-edits on` becomes `aphrollo gate allow primary`.
- README: section at line 580 (`TDD + law gates`) and the hook table naming `premergecommit`; add one paragraph "Waivers" under it.
- tests: new `internal/cli/gate_alias_test.go`, new `internal/cli/allow_test.go`, `internal/tdd/install_test.go` (existing, hook table), and a primaryedits test file in `internal/tdd/` (existing if present, else new).

Tests:
- `TestGatePremerge_IsTheSameRoutineAsPremergecommit`: both spellings reach the same function (route through a seam recording the routine name), and stdout of the routine's first line starts `gate premerge:`.
- `TestInstall_WiresPreMergeCommitToGatePremerge`: the shim body written for `pre-merge-commit` contains `gate premerge` and not `premergecommit`.
- `TestAllowPrimary_WaivesForTheSessionAndRevokeRestores`: with `CLAUDE_CONFIG_DIR` at a temp dir and a fixed session id, `runGateAllow(["primary"])` exit 0 and stdout exactly `Primary-checkout edits ALLOWED for this session — the merge-only rule is waived. Run `aphrollo gate revoke primary` to restore it.`; `PrimaryMergeOnly`'s waiver predicate reports waived; `runGateRevoke(["primary"])` restores; stdout of a bare `runGateAllow(nil)` after allow is `primary since <RFC3339> by <session>` and after revoke `no waivers`.
- `TestAllow_RefusesAnUnknownWall`: `runGateAllow(["nonsense"])` exit 2, stderr `usage: aphrollo gate allow [primary]`.
- `TestPrimaryEditsOn_IsAnAliasOfAllowPrimary`: `primary-edits on` produces the same state and the same stdout as `allow primary`.
- `TestPrimaryRefusal_NamesAllowPrimary`: `PrimaryMergeOnlyReason(root)` contains `aphrollo gate allow primary` and not `primary-edits`.

Interfaces (used by #343 later):
- `tdd.AllowWall(wall string) (msg string, err error)` (named `AllowWall`, not `Allow` — that name is already the Action constant), `tdd.Revoke(wall string) (msg string, err error)`, `tdd.ListWaivers() []tdd.Waiver`, `tdd.Waived(wall string) bool`; `wall` is one of the constants `tdd.WallPrimary = "primary"` (and later `tdd.WallDiscard`).

## B2: install, check and issue at top level (#342, part 2)

Files:
- modify `internal/cli/cli.go` lines 70-100 (top-level switch) and 28-45 (usage): add `install`, `check`, `issue`; `install` runs today's `runGateInit` then `runGateInstall` for `--repo` in that order with both flag sets merged (`-repo`, `-bin`, `-config-dir`, `-git-hooks-dir`, `-cargo-shim-dir`, `-no-git`, `-uninstall`, `-claude-md`, `-ratchet-readme`); `check` lives in a new file `internal/cli/check.go`; `issue` reuses the function behind line 438.
- gate usage lines 218-300: mark `init`, `install`, `issue` as `(alias of aphrollo <verb>; retiring next release)`.
- the new check file: `runCheck(args, stdout, stderr) int` running in order: ratchet (`ratchet.Check` with tighten off), docs (`docs.Check`), sqlc (`sqlc.Check` over discovered configs, `[skip]` when none), doctor (the function behind `gate doctor`), app trio (the function behind `workspace verify` when the workspace declares an app, else `[skip]`). One line per guard: `check: ratchet → clean (6 law(s), 561 file(s))`, `check: docs → clean`, `check: sqlc → [skip] no sqlc config`, `check: doctor → clean`, `check: app trio → [skip] no app declared`; on a miss `check: <guard> → <n> miss(es)` and exit 1 after running the rest.
- README: new section "Judge the tree — `aphrollo check`" after line 543; the Setup section at line 1721 renamed to `aphrollo install`; the issue paragraph under `gate` moved to a top-level "Open points — `aphrollo issue`" section.
- tests: new files `internal/cli/check_test.go`, `internal/cli/install_test.go`, `internal/cli/issue_alias_test.go`.

Tests:
- `TestCheck_RunsEveryGuardAndReportsTheFirstMissWithoutStopping`: fixture repo with a ratchet law that misses once and a dangling doc citation; expect exit 1, stdout containing `check: ratchet → 1 miss(es)` and `check: docs → 1 miss(es)` and `check: sqlc → [skip] no sqlc config` in that order.
- `TestCheck_IsCleanOnACleanRepo`: fixture with no laws, no docs misses: every line ends `clean` or `[skip]`, exit 0.
- `TestInstall_PerformsInitThenInstallWrites`: temp config dir, hooks dir, shim dir and a repo; expect settings.json patched, the hooks dir populated, the repo's `.git/hooks` shims present, in one run.
- `TestGateInitAndGateInstall_StillWork`: same writes through the old spellings, and the gate usage text lists them with `alias of aphrollo install`.
- `TestIssue_TopLevelReachesTheSameFunctionAsGateIssue`: through a seam recording the gh argv, `issue` and `gate issue` produce identical argv.

Interfaces:
- `runCheck(args []string, stdout, stderr io.Writer) int`, `runInstall(args []string, stdout, stderr io.Writer) int` in package cli.

## B3: workspace verbs (#346, carries `Closes #346`)

Files:
- modify `internal/cli/cli.go` lines 1079-1208 (`workspaceUsage`, `runWorkspace`): `rebase` beside `update`; `remove` gains `--keep-branch`; `prune <repo> <branch>` routes to remove with `--keep-branch`; `sync` with no argument resolves the cwd's repo the way `commit` does; `verify` prints `workspace verify is now aphrollo check` and calls `runCheck`; usage text updated, aliases marked.
- modify `internal/workspace/prune.go`: `--stale <dur>` flag; a candidate is a detached tree (no branch) with no PR, no `<tree>.lane` marker beside it, last commit older than dur, newest file mtime older than dur, and clean; `--dry` lists. Reuse `removeWorktree` (line 370).
- modify `internal/workspace/status.go` or the list function: `list` prints `path  branch|detached  <n>d  <n> dirty  PR <state>`; PR state comes from the existing gh seam, `none` when no PR.
- README: sections at lines 270 (coder verbs), 338 (Verify), 412 (sync), 444 (merge), 467 (prune).
- tests: `internal/workspace/prune_test.go`, `internal/workspace/status_test.go`, `internal/workspace/remove_test.go`, `internal/workspace/sync_test.go` (all existing), and a new file `internal/cli/workspace_alias_test.go`.

Tests:
- `TestPrune_StaleRemovesADetachedPRLessTreeOlderThanTheBar`: detached tree with a commit dated 5 days ago (set `GIT_COMMITTER_DATE`), files touched 5 days ago (`os.Chtimes`), no marker; `--stale 3d` removes it; the same tree at 1 day is kept with a `skip:` line naming the age.
- `TestPrune_StaleKeepsADirtyDetachedTree`: same but with an uncommitted edit; kept, `skip: ... dirty`.
- `TestPrune_StaleKeepsATreeWithALaneMarker`: `<tree>.lane` file beside it; kept.
- `TestList_ShowsAgeDirtyAndPRState`: two trees; expect lines matching `<path>  lane/x  0d  1 dirty  PR none` and `<path>  detached  5d  0 dirty  PR none` (gh seam returns none).
- `TestRemove_KeepBranchLeavesTheBranch`: after `remove --keep-branch`, `git branch --list <b>` is non-empty; without the flag it is empty.
- `TestWorkspaceUpdate_IsAnAliasOfRebase`, `TestWorkspacePruneTicketForm_IsRemoveKeepBranch`, `TestWorkspaceVerify_PointsAtCheck`: through the dispatch seam, same target function; verify's first stdout line is exactly `workspace verify is now aphrollo check`.
- `TestSync_DefaultsToTheCwdRepo`: run from inside a clone with no argument; syncs that clone.

Interfaces: none new beyond flags.

## B4: CLAUDE.md command surface stays honest (#342, part 3; carries `Closes #342`)

Files:
- new file `internal/cli/surface.go`: `TopLevelVerbs() []string`, `GateVerbs() []string`, `WorkspaceVerbs() []string`, each derived from the same tables the dispatch switches read (refactor the switches to range over a table if they are literal cases; keep behaviour byte-identical).
- new file `internal/cli/claudemd_surface_test.go`: reads the repo's `CLAUDE.md` (walk up from the test's working directory to the module root), extracts every verb named in the "Command surface" section (`refactor`, `workspace` bullets, `gate` bullets, the `dev` line, etc.), and asserts each is in the matching table and each top-level verb is mentioned.
- modify `CLAUDE.md` "Command surface": replace `prepare/claim/unclaim/list/remove/prune/cleanup` and `commit/push/pr/ship/merge` with the live lists; add `install`, `update`, `check`, `issue`, `version`.
- tests: the surface test above; a new file `internal/cli/surface_test.go` asserting the tables mark aliases as such (`Alias bool`).

Tests:
- `TestClaudeMD_CommandSurfaceNamesOnlyLiveVerbs`: fails on today's CLAUDE.md naming `prepare`, `cleanup`, `pr`, `ship`; passes after the edit.
- `TestClaudeMD_CommandSurfaceNamesEveryTopLevelVerb`: each of `TopLevelVerbs()` appears in the section.
- `TestSurface_TablesMatchDispatch`: for each verb in each table, `Run([]string{verb, "--help"})` (or the sub-runner) exits 0 or 2, never "unknown command".

Interfaces: `cli.TopLevelVerbs()`, `cli.GateVerbs()`, `cli.WorkspaceVerbs()`.
