# Plan: binary lifecycle

Five lanes. Wave 1: L1, L2, L3 are independent. Wave 2: L4 and L5 depend on L3's interface and start after it merges. Every lane branches from `origin/main`, commits one mixed test+impl commit under the tdd skill, and carries its `Closes` trailer only where noted (an issue closes on the merge of its LAST lane).

Repo-wide facts every builder needs: `cargo` and `git` resolve to the queue shim; the hooks run the tests and print one `gate:` line per edit; the primary checkout is merge-only, work only in the lane worktree you are handed.

## L1: gate init skips the managed block in a merge-only primary (#345, part 1)

Files:
- modify `internal/tdd/claudemd.go` lines 128-159 (`WriteClaudeMD`): before reading the file, `if _, ok := PrimaryMergeOnly(repoRoot); ok { return false, ErrManagedBlockInPrimary }`. Add the sentinel beside it.
- modify `internal/cli/cli.go` lines 898-906 (the `WriteClaudeMD` call in `runGateInit`): on `errors.Is(err, tdd.ErrManagedBlockInPrimary)` print the notice to stdout and continue with exit 0; every other error keeps today's handling.
- tests: new `internal/tdd/claudemd_primary_test.go` and `internal/cli/gateinit_primary_test.go`.

Tests:
- `TestWriteClaudeMD_LeavesAMergeOnlyPrimaryByteIdentical`: `makeGoRepo`, `addWorktree(t, root, "lane-a")` (both helpers exist in the tdd test files), write a CLAUDE.md containing an OLD managed block (any text between the markers `ClaudeMDBlock` uses, differing from the current template). Call `WriteClaudeMD(root, shimDir, false)`. Expect `changed == false`, `errors.Is(err, ErrManagedBlockInPrimary)`, and the file's bytes equal to what was written.
- `TestWriteClaudeMD_StillWritesTheBlockInALaneOfThatRepo`: same setup, call against the lane path with an old block in the lane's CLAUDE.md. Expect `changed == true`, `err == nil`, and the file to contain `ClaudeMDBlock(shimDir, false)` verbatim.
- `TestWriteClaudeMD_StillWritesInASingleCheckoutClone`: `makeGoRepo` with no linked worktree, old block. Expect `changed == true`. (The rule fires only with a linked worktree; a plain clone is untouched.)
- `TestGateInit_PrintsOneNoticeAndExitsZeroInAMergeOnlyPrimary`: drive `runGateInit` with `--repo <primary>` and a config dir under `t.TempDir()`. Expect exit 0 and stdout to contain exactly one line equal to `gate init: CLAUDE.md managed block is behind the template in the merge-only primary; land it through a lane (aphrollo gate init --repo <lane>)` with `<lane>` literal.

Interfaces:
- `var ErrManagedBlockInPrimary = errors.New("managed CLAUDE.md block not written: merge-only primary checkout")` in `internal/tdd/claudemd.go`.

## L2: workspace sync lets git decide the fast-forward (#345, part 2; carries `Closes #345`)

Files:
- modify `internal/workspace/sync.go` lines 49-70: delete the dirty pre-check (the `git status --porcelain` branch that prints "is checked out with uncommitted changes"). Keep the `--dry` line. Run `git merge --ff-only <remote>` as today; when it fails, print `<def> could not fast-forward: ` followed by git's first stderr line, return nil (non-destructive, exit 0), never return an error for that case.
- update the header comment lines 17-28 to state the new rule: git refuses a fast-forward only when it would overwrite a dirty path, so sync asks git and reports git's reason.
- tests: `internal/workspace/sync_test.go` (existing; add two cases beside the current ones, keep the existing ones green).

Tests:
- `TestSync_FastForwardsPastADirtyFileTheUpdateDoesNotTouch`: origin bare repo, clone, push one commit from a second clone changing `a.txt`; in the first clone modify `b.txt` without committing. `Sync(clone, false, ...)`. Expect `rev-parse HEAD == rev-parse origin/main`, `b.txt` still holding the uncommitted content, stdout containing `fast-forwarded main to origin/main (1 commit(s))`.
- `TestSync_LeavesATreeWhoseDirtyFileTheUpdateTouches`: same, but the uncommitted edit is to `a.txt`. Expect HEAD unchanged, `err == nil`, stdout containing `main could not fast-forward: ` and the literal git phrase `would be overwritten by merge`.

Interfaces: none new.

## L3: the binary knows its commit (#340, part 1)

Files:
- new leaf package `internal/buildinfo/buildinfo.go`: `var commit, builtAt string` (set by the linker), `func Stamp() (commit, builtAt string, stamped bool)` returning `stamped == false` when `commit == ""`. Imports nothing from the module.
- modify `internal/cli/selfinstall.go` lines 26-50: `buildAphrollo` calls a new pure `buildArgs(repo, sha string, now time.Time) []string` that returns `["build", "-buildvcs=false", "-ldflags", "-X github.com/aphrollo/aphrollo-tools/internal/buildinfo.commit=<sha> -X github.com/aphrollo/aphrollo-tools/internal/buildinfo.builtAt=<now RFC3339 UTC>", "-o", out, "./cmd/aphrollo"]`; the sha comes from `git -C repo rev-parse HEAD` (a failure leaves both `-X` values empty, never fails the build).
- modify `internal/cli/cli.go`: add top-level verb `version` to the dispatch and the usage list (one line: `version     Print the commit and build time this binary was stamped with`).
- new `internal/cli/version.go`: `runVersion(stdout io.Writer) int`.
- tests: new `internal/buildinfo/buildinfo_test.go` and `internal/cli/version_test.go`; `internal/cli/selfinstall_test.go` (existing; add the args case).

Tests:
- `TestStamp_ReportsUnstampedWhenTheLinkerSetNothing`: with the package vars at their zero values, `Stamp()` returns `("", "", false)`.
- `TestVersion_PrintsUnstampedWithoutALinkerStamp`: `runVersion` output is exactly `aphrollo (unstamped)\n`.
- `TestVersion_PrintsShortShaAndBuildTime`: set the vars (test in package buildinfo sets them through an exported test hook `SetForTest(commit, builtAt string)` that panics unless `testing.Testing()`), expect exactly `aphrollo ca47dba built 2026-09-05T02:57:00Z\n` for commit `ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2` and builtAt `2026-09-05T02:57:00Z`.
- `TestBuildArgs_StampsCommitAndBuildTimeThroughLdflags`: `buildArgs("/r", "ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2", time.Date(2026, 9, 5, 2, 57, 0, 0, time.UTC))` with out `/o` equals the literal slice above with `-o /o` last before `./cmd/aphrollo`.

Interfaces (used by L4 and L5):
- `buildinfo.Stamp() (commit, builtAt string, stamped bool)`
- `buildinfo.SetForTest(commit, builtAt string)`
- `buildArgs(repo, sha string, now time.Time) []string` in package cli (unexported; L4 does not call it, it calls `buildAphrollo`).

## L4: aphrollo update (#340, part 2)

Files:
- modify `internal/cli/selfinstall.go` lines 55-120: extract the rename-aside, move-into-place and sweep steps (today lines 78-107 of the file) into `swapBinary(prefix, bin, staged string, stdout io.Writer) (stale string, err error)` printing the same three lines with the caller's prefix so both verbs print their own name; `runGateSelfInstall` calls it and behaves byte-identically.
- new `internal/cli/update.go`: `runUpdate(args []string, stdout, stderr io.Writer) int`. Flags: `--repo` (default `.`; must contain a `go.mod` whose module line is `github.com/aphrollo/aphrollo-tools`, else exit 2 naming the flag), `--bin` (default this executable), `--no-init`, `--remote` (default `origin`), `--branch` (default `main`). Steps, one line each on stdout prefixed `aphrollo update: `: `git -C repo fetch <remote>`; `head := git rev-parse <remote>/<branch>`; if `buildinfo.Stamp()` commit equals head print `already at <sha7> [skip]` and exit 0; `git worktree add --detach <tmp> <remote>/<branch>` with tmp under `os.MkdirTemp`; `buildAphrollo(tmp, staged)`; always `git worktree remove --force <tmp>` (deferred, runs on the failure path too); `swapBinary`; then `runGateInit(--bin bin ...)` unless `--no-init`.
- modify `internal/cli/cli.go`: top-level dispatch entry `update` and the usage line `update      Fetch, build ./cmd/aphrollo from origin/main in a temporary worktree, swap it in, sweep stale copies, re-run init (--repo, --bin, --no-init)`.
- tests: new `internal/cli/update_test.go`; `internal/cli/selfinstall_test.go` (existing; the extracted swap keeps its cases green).

Tests (all through the `buildAphrollo` seam, which records the `repo` it was handed and writes a fixed byte string to `out`):
- `TestUpdate_SkipsWhenTheBinaryIsAlreadyAtOriginMain`: bare origin plus clone; `buildinfo.SetForTest(<origin/main sha>, ...)`. Expect exit 0, stdout exactly one line `aphrollo update: already at <sha7> [skip]`, the seam never called, the bin file's bytes unchanged.
- `TestUpdate_BuildsFromADetachedWorktreeAtOriginMainNotTheWorkingTree`: clone is one commit behind origin and has an uncommitted edit. Expect the seam's `repo` to be a path other than the clone, `git -C <repo> rev-parse HEAD` equal to `origin/main`, `git -C <repo> status --porcelain` empty at build time (the seam records it), and the clone's uncommitted edit untouched afterwards.
- `TestUpdate_RemovesTheTemporaryWorktreeEvenWhenTheBuildFails`: seam returns an error. Expect exit 1, the recorded `repo` path to no longer exist, `git worktree list` of the clone to name only the clone, and the bin file unchanged.
- `TestUpdate_SwapsAndSweepsLikeSelfInstall`: pre-create a stale sibling named with the `.stale-` prefix beside the bin. Expect the bin's bytes to equal the seam's fixed output, exactly one new `.stale-` sibling holding the old bytes, and the pre-existing stale copy gone.
- `TestUpdate_RefusesARepoThatIsNotAphrolloTools`: `--repo` pointing at a module named otherwise. Expect exit 2 and stderr naming `--repo`.

Interfaces:
- `swapBinary(prefix, bin, staged string, stdout io.Writer) (stale string, err error)` in package cli.

## L5: the session says when the binary is behind (#340, part 3; carries `Closes #340`)

Files:
- new `internal/tdd/behind.go`: `func BinaryBehindLine(now time.Time) string`. Reads `buildinfo.Stamp()`; unstamped returns `""`. Reads the cache file `<stateDir>/binary-behind.json` (`{"checked_at": RFC3339, "head": sha}`); when `now - checked_at < 1h` uses the cached head, else calls `lsRemoteFn(ctx)` with a 2 s `context.WithTimeout` running `git ls-remote --heads https://github.com/aphrollo/aphrollo-tools main`, writes the cache on success, returns `""` on any error or timeout. Returns `""` when head equals the stamped commit, else the line `aphrollo binary is behind origin/main (built at <sha7>, origin at <sha7>): run aphrollo update`.
- modify `internal/tdd/session.go` lines 279-320 (`HandleSessionStart`): after the weekly digest is appended, `if line := BinaryBehindLine(time.Now()); line != "" { parts = append(parts, line) }`.
- tests: new `internal/tdd/behind_test.go`.

Tests (through `var lsRemoteFn = func(ctx context.Context) (string, error)`, and `CLAUDE_CONFIG_DIR` pointed at `t.TempDir()` so the cache is private):
- `TestBinaryBehindLine_SilentWhenUnstamped`: seam would return a head; expect `""` and the seam never called.
- `TestBinaryBehindLine_NamesBothShasWhenOriginMoved`: stamp `ca47dba1...` (40 hex), seam returns `15ac7910...` (40 hex). Expect exactly `aphrollo binary is behind origin/main (built at ca47dba, origin at 15ac791): run aphrollo update`.
- `TestBinaryBehindLine_SilentAtHead`: seam returns the stamped sha. Expect `""`.
- `TestBinaryBehindLine_AsksTheRemoteOncePerHour`: two calls at `t0` and `t0+59m` yield one seam invocation; a third at `t0+61m` yields a second.
- `TestBinaryBehindLine_SilentWhenTheRemoteExceedsTheBudget`: seam blocks until `ctx.Done()` then returns `ctx.Err()`. Expect `""` and the call to return within 2.5 s measured by the test.
- `TestHandleSessionStart_CarriesTheBehindLine`: with the seam returning a different head and a stamp set, `HandleSessionStart` output contains the line once.

Interfaces:
- `BinaryBehindLine(now time.Time) string` in package tdd; `lsRemoteFn` seam.
