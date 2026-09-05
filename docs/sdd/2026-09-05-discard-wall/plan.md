# Plan: discard wall

Two lanes. C1 is pure classification and measurement with no wiring and can start any time. C2 wires the wall into the shim and needs both C1 and the verb-surface tree's B1 (`tdd.Allow`, `tdd.Revoke`, `tdd.Waived`, `tdd.ListWaivers`) merged. Every lane branches from `origin/main`, commits one mixed test+impl commit under the tdd skill, and updates README in the same commit.

Repo-wide facts every builder needs: `cargo` and `git` resolve to the queue shim; the hooks run the tests and print one `gate:` line per edit; the primary checkout is merge-only, work only in the lane worktree you are handed. The shim's entry is `runGitShim` in `internal/cli/git_shim.go` (line 133); the primary wall is consulted at line 145 via `primaryRefusalLine`; `gitGlobalArgs` (line 349) splits `-C`/`-c` prefixes from the verb and its args. Overrides are logged with `logOverride` in `internal/tdd/denylog.go` (line 40) and counted by `internal/tdd/stats.go` under the prefixes at line 130.

## C1: classify a discarding invocation and measure what it would destroy

Files:
- new file `internal/cli/git_shim_discard.go`: `discardIntent(rest []string) (form string, paths []string, ok bool)` and `discardCost(realGit, workDir, form string, paths []string) discardCost` with `type discardCost struct { Files, Insertions, Deletions, Untracked, Stashes, UnmergedCommits int; Worktree string }` and `func (c discardCost) zero() bool`; `discardRefusalLine(form string, c discardCost) string` rendering exactly the lines the spec's acceptance criteria show.
- tests: new file `internal/cli/git_shim_discard_test.go`.

Classification table (`form` is the spelling the refusal prints):
- `reset` with `--hard` or `--merge` → `reset --hard` / `reset --merge`, whole tree; `reset` without either → not ok.
- `checkout` with `--` followed by paths, or with `.`, or with `-f`/`--force` → `checkout -- <paths>` (paths after `--`, or `.`); `checkout -f <branch>` → `checkout -f`, whole tree. `checkout <branch>` without those → not ok (the primary wall owns branch moves).
- `restore` without `--staged` (or with `--worktree`) → `restore <paths>`; `restore --staged` alone → not ok; `restore --staged --worktree` → ok.
- `clean` with `-f`/`--force` (any combination such as `-fd`, `-fdx`, `-ff`) and without `-n`/`--dry-run` → `clean <flags as typed>`; untracked count over the paths named or the whole tree; `-x` adds ignored files to the count.
- `stash drop` / `stash clear` → `stash drop` / `stash clear`; count stash entries (`git stash list | wc -l`), or 1 for `drop <ref>` when the ref exists.
- `branch -D <b>` / `branch --delete --force <b>` / `branch -d -f <b>` → `branch -D <b>`; unmerged = `git rev-list --count HEAD..<b>`.
- `worktree remove --force <path>` (or `-f`) → `worktree remove --force`; cost measured inside `<path>` (`Worktree` set).
- Everything else → not ok.

Measurement (`discardCost`):
- Files and +/-: `git diff --shortstat HEAD -- <paths>` (falls back to `git diff --shortstat -- <paths>` in a repo with no HEAD); `Files` is the "N files changed" number, insertions and deletions parsed from the same line, zero when absent.
- Untracked: `git ls-files --others --exclude-standard -- <paths>` line count; with `-x` drop `--exclude-standard`.
- Stashes: `git stash list` line count.
- UnmergedCommits: `git rev-list --count HEAD..<b>`.
- Any git error → treat as zero for that number (the wall never blocks on its own failure to measure; a refusal needs evidence).

Tests (fixture: `t.TempDir()` git repo with one commit; helpers may be copied from the shim's existing tests, see `internal/cli/git_shim_test.go`):
- `TestDiscardIntent_ClassifiesEachForm`: table with these exact inputs and expected `(form, paths, ok)`: `["reset","--hard"]` → `("reset --hard", nil, true)`; `["reset","--soft","HEAD~1"]` → `(_, _, false)`; `["checkout","--","a.txt","b.txt"]` → `("checkout -- <paths>", ["a.txt","b.txt"], true)`; `["checkout","."]` → `("checkout -- <paths>", ["."], true)`; `["checkout","main"]` → false; `["checkout","-f","main"]` → `("checkout -f", nil, true)`; `["restore","b.txt"]` → `("restore <paths>", ["b.txt"], true)`; `["restore","--staged","b.txt"]` → false; `["clean","-fd"]` → `("clean -fd", nil, true)`; `["clean","-n"]` → false; `["clean","-fdn"]` → false; `["stash","drop"]` → `("stash drop", nil, true)`; `["stash","push"]` → false; `["branch","-D","x"]` → `("branch -D x", nil, true)`; `["branch","-d","x"]` → false; `["worktree","remove","--force","/w"]` → `("worktree remove --force", ["/w"], true)`; `["worktree","remove","/w"]` → false; `["status"]` → false.
- `TestDiscardCost_CountsModifiedFilesAndDiffNumbers`: write three tracked files with known edits producing +212/-40 in total (generate lines deterministically), two untracked files; expect `Files == 3, Insertions == 212, Deletions == 40, Untracked == 2`.
- `TestDiscardCost_RestrictsToTheNamedPaths`: `a.txt` +5/-1 and `b.txt` +7/-2 modified; `discardCost(..., "restore <paths>", ["b.txt"])` → `Files == 1, Insertions == 7, Deletions == 2`.
- `TestDiscardCost_IsZeroOnACleanTree`: `zero()` true for `reset --hard` on the fixture with no edits.
- `TestDiscardCost_CountsUnmergedCommitsForBranchDelete`: branch `x` with 2 commits off HEAD → `UnmergedCommits == 2`; a branch at HEAD → 0.
- `TestDiscardCost_CountsStashEntries`: one `git stash push` → `Stashes == 1`.
- `TestDiscardRefusalLine_RendersEachShape`: `("reset --hard", {Files:3, Insertions:212, Deletions:40, Untracked:2})` → `gate: refused — reset --hard discards 3 file(s), +212/-40 uncommitted; aphrollo gate allow discard arms one command, APHROLLO_DISCARD=1 for scripts` (untracked is not printed for reset: git's reset --hard leaves untracked files alone); `("clean -fd", {Untracked:2})` → `gate: refused — clean -fd discards 2 untracked file(s); aphrollo gate allow discard arms one command, APHROLLO_DISCARD=1 for scripts`; `("stash drop", {Stashes:1})` → `... stash drop discards 1 stash entry(ies); ...`; `("branch -D x", {UnmergedCommits:2})` → `... branch -D x discards 2 unmerged commit(s); ...`; `("worktree remove --force", {Files:1, Insertions:3, Deletions:1, Worktree:"/w"})` → `... worktree remove --force discards 1 file(s), +3/-1 uncommitted in /w; ...`.

Interfaces (used by C2):
- `discardIntent(rest []string) (form string, paths []string, ok bool)`
- `discardCost(realGit, workDir, form string, paths []string) discardCost` and `(discardCost).zero() bool`
- `discardRefusalLine(form string, c discardCost) string`

## C2: wire the wall, the one-shot waiver and the env override into the shim (carries `Closes #343`)

Files:
- modify `internal/cli/git_shim.go` around line 145: after the primary wall and before any lock, `if form, paths, ok := discardIntent(rest); ok { ... }`: compute cost with `workDir` (for `worktree remove --force` measure inside the named path); if not zero: if `os.Getenv("APHROLLO_DISCARD") == "1"` → `tdd.LogOverride("override-discard-env", ...)` and pass; else if `tdd.ConsumeOneShot(tdd.WallDiscard)` → log `override-discard-used` and pass; else print `discardRefusalLine` to stderr, `tdd.AppendGateLog("git", workDir, "git", "git-discard-refused:"+formKey, 0)` where `formKey` is the form with spaces replaced by `-`, and return 1 without running git.
- modify `internal/tdd/primaryedits_cli.go` (or wherever B1 put the waiver state): add `WallDiscard = "discard"` with one-shot semantics: `Allow(WallDiscard)` writes `{armed: true, until: now+5m}` for the session and prints `Discard ARMED for one command in this session (until <RFC3339>). Run aphrollo gate revoke discard to disarm.`; `ConsumeOneShot(wall) bool` returns true once and clears the state, false when absent or expired; `ListWaivers` renders `discard armed until <RFC3339> by <session>`.
- modify `internal/tdd/stats.go` line 130 prefixes: add `git-discard-refused:`.
- README: under the "Git queue (`aphrollo gate git`)" section (line 960) add "The discard wall" with the refusal line, the two overrides and the stats rows; add `discard` to the Waivers paragraph B1 wrote.
- tests: new file `internal/cli/git_shim_discard_wall_test.go`, `internal/tdd/primaryedits_test.go` or the waiver test file B1 created (add the one-shot cases), `internal/tdd/stats_test.go` (existing; the new prefix).

Tests (drive `runGitShim` with a `gitShimConfig` pointing at real git and a fixture repo, as the existing shim tests do):
- `TestGitShim_RefusesResetHardWithUncommittedWork`: 3 modified files (+212/-40); expect exit 1, stderr exactly the spec's line 1, the files still modified, and gate.log containing `git-discard-refused:reset---hard`.
- `TestGitShim_PassesResetHardOnACleanTree`: exit 0, no stderr, git ran (a marker: `HEAD` unchanged and no refusal text).
- `TestGitShim_RestoreMeasuresOnlyTheNamedPath`: `a.txt` and `b.txt` modified; `restore b.txt` refused with `discards 1 file(s)`; `restore --staged b.txt` passes.
- `TestGitShim_CleanForceRefusedDryRunPasses`: `clean -fd` refused with `2 untracked file(s)`; `clean -n` passes.
- `TestGitShim_BranchDeleteForceRefusedWhenUnmerged`: `branch -D x` with 2 unmerged commits refused; after merging `x` into HEAD it passes.
- `TestGitShim_OneShotAllowPassesExactlyOneCommand`: `tdd.Allow(WallDiscard)`; first `reset --hard` passes and gate.log has `override-discard-used`; a second `reset --hard` (after re-dirtying) is refused; `ListWaivers()` is empty after the first.
- `TestGitShim_OneShotExpiresAfterFiveMinutes`: arm with a clock seam at `t0`, attempt at `t0+5m+1s` → refused, waiver gone.
- `TestGitShim_EnvOverridePassesAndIsLogged`: `t.Setenv("APHROLLO_DISCARD","1")`; passes; gate.log has `override-discard-env`.
- `TestStats_CountsDiscardRefusalsAndOverrides`: a gate.log with two `git-discard-refused:reset---hard`, one `override-discard-used`, one `override-discard-env`: the rendered stats show the refusal count 2 and both override rows under denies / overrides.

Interfaces: `tdd.WallDiscard`, `tdd.ConsumeOneShot(wall string) bool`, plus B1's `Allow`/`Revoke`/`ListWaivers`.
