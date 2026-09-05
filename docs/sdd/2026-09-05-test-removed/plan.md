# Plan: test removal law

Two lanes, D1 then D2 (D2 needs the kind to exist). Every lane branches from `origin/main`, commits one mixed test+impl commit under the tdd skill, and updates README in the same commit.

Repo-wide facts every builder needs: `cargo` and `git` resolve to the queue shim; the hooks run the tests and print one `gate:` line per edit; the primary checkout is merge-only, work only in the lane worktree you are handed. The engine: `internal/ratchet/check.go` (`Options` at line 56, `Check` at line 139), whole-tree kinds in `internal/ratchet/wholetree.go`, kind schema in `internal/ratchet/law_matcher_fields.go`, fixtures in `internal/ratchet/fixtures.go`, gate entry `ratchetStage` in `internal/tdd/ratchetgate.go` line 166, CLI in `internal/cli/ratchet.go`.

## D1: the `symbol-removed` kind, a base for the engine, and fixtures with a base

Files:
- modify `internal/ratchet/check.go` `Options` (line 56): add `Base string` (a git ref; empty means unknown) and `BaseTree BaseReader` (nil means derive from `Base` via git; fixtures pass a file-backed one). Add to `Result.Notes` the skip note when a diff kind runs without a base.
- new file `internal/ratchet/basetree.go`: `type BaseReader interface { List() ([]string, error); Read(path string) ([]byte, error) }`; `gitBaseReader{root, ref}` using `git -C root ls-tree -r --name-only <ref>` and `git -C root show <ref>:<path>`; `dirBaseReader{dir}` walking a directory.
- modify `internal/ratchet/wholetree.go`: add `symbol-removed` to `wholeTreeKinds`; implementation: for every scope file at base, collect capture-group-1 names from `pattern`; for every scope file at tip (tree plus `Proposed` overlay), collect the same; also collect tombstones matching `(?m)^\s*(?://|#)\s*ratchet:\s*<law name>\s+([A-Za-z0-9_]+):\s*\S` (name in group 1, requires non-empty reason). Hit for every base name absent from tip names and tombstone names, keyed `<base path>:<name>`, remedy `test removed without a tombstone; add `// ratchet: <law> <name>: <why>` where it stood, or restore it`.
- modify `internal/ratchet/law_matcher_fields.go`: `symbol-removed` requires `pattern` with exactly one capture group; validation error otherwise.
- modify `internal/ratchet/fixtures.go`: when a fixture's `hit/` or `clean/` directory contains `base/` and `tip/`, judge `tip/` with `BaseTree = dirBaseReader(base/)`; otherwise behave as today.
- modify `internal/cli/ratchet.go`: `check` gains `--base <ref>`.
- modify `internal/tdd/ratchetgate.go` line 171: `ratchetStage` passes `Base: "HEAD"` for both `precommit` and `premergecommit`; the edit-time call (line 88) passes none.
- README: the ratchet section gains a "Diff-scoped kinds" paragraph and the `--base` flag in the `ratchet check` usage.
- tests: `internal/ratchet/wholetree_symbol_removed_test.go` (new), `internal/ratchet/basetree_test.go` (new), `internal/ratchet/fixtures_test.go` (existing; base/tip case), `internal/cli/ratchet_test.go` (existing or new; `--base`), `internal/tdd/ratchetgate_test.go` (existing; Base passed).

Tests (fixture repos built with git in `t.TempDir()`; law text inline with `pattern = "^func (Test[A-Za-z0-9_]+)\\("`):
- `TestSymbolRemoved_ReportsATestPresentAtBaseAndGoneAtTip`: base commit has `a_test.go` with `TestFoo` and `TestBar`; tip deletes `TestFoo`. Expect exactly one finding with key `a_test.go:TestFoo`.
- `TestSymbolRemoved_IgnoresAMoveByName`: tip moves `TestFoo` verbatim into `b_test.go`. Expect no finding.
- `TestSymbolRemoved_ReportsARenameAsTheOldName`: tip renames `TestFoo` to `TestFooBar`. Expect one finding `a_test.go:TestFoo`.
- `TestSymbolRemoved_TombstoneWithReasonAdmitsTheRemoval`: tip deletes `TestFoo` and adds `// ratchet: test_removed TestFoo: covered by TestFooTable since the table form landed`. Expect none. Variant `// ratchet: test_removed TestFoo:` (no reason) expects the finding.
- `TestSymbolRemoved_SkipsWithoutABaseAndSaysSo`: `Options{}` with no `Base`: no finding, `Result.Notes` contains `test_removed: skipped, no base (pass --base <ref>)`.
- `TestSymbolRemoved_ReadsTheTipFromTheProposedOverlay`: base has `TestFoo`; tree still has it; `Proposed` overlay for `a_test.go` lacks it. Expect the finding (this is the staged-index case).
- `TestGitBaseReader_ListsAndReadsTheRefTree`: two files at HEAD; `List()` returns both paths sorted; `Read` returns the committed bytes even when the worktree copy differs.
- `TestFixtures_JudgeTipAgainstBaseWhenBothDirsExist`: a fixture dir with `hit/base/a_test.go` (TestFoo) and `hit/tip/a_test.go` (no TestFoo), `expected.txt` = `a_test.go:TestFoo`; `RunFixtures` reports ok. Remove `expected.txt`'s line → reports the mismatch.
- `TestLawMatcherFields_SymbolRemovedNeedsOneCaptureGroup`: pattern without a group → validation error naming the law and `capture group`.
- `TestRatchetStage_PassesHeadAsTheBase`: through the `ratchetCheckFn` seam, both gate names set `Options.Base == "HEAD"`.
- `TestRatchetCheckCLI_BaseFlagReachesOptions`: `ratchet check --base HEAD~1` sets `Options.Base`.

Interfaces (used by D2):
- law TOML: `kind = "symbol-removed"`, `pattern = "<regex with one capture group>"`; the tombstone marker is fixed by the engine as `ratchet: <law name> <symbol>: <reason>`.
- fixture layout: `<fixture>/hit/base/...`, `<fixture>/hit/tip/...`, `<fixture>/clean/base/...`, `<fixture>/clean/tip/...`, `expected.txt` with `<path>:<name>` rows.

## D2: presets and this repo's own law (carries `Closes #344`)

Files:
- new `internal/ratchet/presets/common/test_removed.toml`: name `test_removed`, severity deny, description stating the rule and the tombstone form, `kind = "symbol-removed"`, `pattern = "{{pattern}}"`, scope include `{{include}}`.
- new `internal/ratchet/presets/go/test_removed.toml`: extends common with `pattern = "^func (Test[A-Za-z0-9_]+)\\("`, include `**/*_test.go`.
- new `internal/ratchet/presets/rust/test_removed.toml`: extends common with `pattern = "#\\[(?:tokio::)?test\\]\\s*(?:async\\s+)?fn\\s+([A-Za-z0-9_]+)"`, include `**/*.rs`, exclude `**/target/**`.
- new `.ratchet/laws/test_removed.toml` in this repo: `extends = "go/test_removed"`, severity deny.
- new fixtures under `.ratchet/fixtures/test_removed/`: `hit/base/internal/x/a_test.go` with `TestFoo` and `TestBar`, `hit/tip/internal/x/a_test.go` with `TestBar` only, `clean/base/internal/x/a_test.go` with `TestFoo`, `clean/tip/internal/x/b_test.go` with `TestFoo` verbatim, `expected.txt` = `internal/x/a_test.go:TestFoo`.
- README: the preset table gains the three entries; `.ratchet/README.md` (if this repo tracks one) gains the law.
- tests: `internal/ratchet/preset_test.go` (existing; the three presets render and validate), plus `aphrollo ratchet test` green as the evidence for the fixtures.

Tests:
- `TestPresets_TestRemovedRendersForGoAndRust`: `go/test_removed` renders to the Go pattern and `**/*_test.go`; `rust/test_removed` to the Rust pattern; both validate (one capture group).
- `TestRustTestRemovedPattern_MatchesPlainTokioAndAsync`: the rendered Rust regex captures `it_works` from `#[test]\nfn it_works()`, `runs` from `#[tokio::test]\nasync fn runs()`, and nothing from `fn helper()`.
- `TestGoTestRemovedPattern_IgnoresBenchmarksAndHelpers`: captures `TestFoo` from `func TestFoo(t *testing.T)`, nothing from `func BenchmarkFoo(` or `func testHelper(`.
- Evidence: `aphrollo ratchet test` prints `ratchet: test_removed ok (1 hit, 1 clean)` in this repo.

Interfaces: none beyond D1's.
