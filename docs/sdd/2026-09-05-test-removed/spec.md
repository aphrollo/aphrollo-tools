# Test removal law: a test that existed at the base and is gone at the tip is a hit unless a tombstone names why

Issue: #344. Independent of the other trees. Spec tree is scaffolding; it is deleted in the merge that lands the last lane.

## Problem

Deleting a test to reach green is a slop class nothing catches: fail-first proves a new test fails without its implementation, and the mutation receipt proves surviving tests kill something, but neither notices a test that was simply removed. Renaming or moving a test is fine and common; silently dropping one is not.

The law engine judges a tree and ratchets ceilings that only go down. A test count is a floor that must only go up, the inverse shape, so a floor row would need new one-way semantics and a way to lower it. Judging the DIFF instead needs no floor: a symbol present at the base and absent at the tip is the hit, and the bar is zero.

## Decisions

- chose a diff-scoped matcher over a floor row, because the engine's one-way property stays "ceilings only go down" and the removed test itself is the evidence; no count to store, no floor to lower (user decision 2026-09-05).
- chose pairing by symbol NAME across the whole scope over per-file comparison, because a moved or renamed-in-place test must not read as removed; a rename that changes the name reads as one removal plus one addition and needs a tombstone, which is correct: the old name's guarantee is gone.
- chose a tombstone comment at the tip as the escape over a commit-message trailer, because every law's escape is source text the next reader sees, and a trailer disappears from the tree.
- chose skipping the kind when no base is known (edit-time single-file runs) over guessing one, because a wrong base invents removals; the commit and merge gates always know their base.
- chose `HEAD` as the base at both precommit and premerge over the lane's merge-base, because precommit judges the staged tree against the last commit and premerge judges the merge result against the trunk it lands on; both are the trees a reviewer compares.
- chose extending the fixture format with `base/` and `tip/` subtrees over exempting diff kinds from fixtures, because a law nobody proved catches nothing; the fixture runner reads the base from files where the gate reads it from git.
- chose a `common/test_removed` preset with a `{{pattern}}` param plus `go/` and `rust/` presets that fill it, over one hard-coded regex, because what a test looks like is per language and the preset library already works this way.

## Boundaries

- No floor, no count, no baseline row for this law.
- Not a judgement of test QUALITY; the mutation receipt owns that.
- Rust `#[cfg(test)]` module removal is judged only through the `#[test]` functions it contained.
- No change to how other kinds run; `Base` is optional and ignored by them.

## Acceptance criteria

1. `ratchet check --base HEAD` in a repo whose staged tree deletes `TestFoo` from `a_test.go` and adds nothing reports one finding keyed `a_test.go:TestFoo` for the law, exit 1.
2. The same tree with `TestFoo` moved verbatim to `b_test.go` reports nothing.
3. The same tree with `TestFoo` renamed to `TestFooBar` reports `a_test.go:TestFoo` (the addition is not the law's business).
4. Deleting `TestFoo` and adding the line `// ratchet: test_removed TestFoo: covered by TestFooTable since the table form landed` anywhere in the scope reports nothing; the same line without text after the colon still reports the hit.
5. An edit-time run (single file, no base) neither reports the kind nor errors; `ratchet check` without `--base` outside a gate does the same and prints one note `test_removed: skipped, no base (pass --base <ref>)`.
6. The precommit ratchet stage judges the index against `HEAD`; the premerge stage judges the merge result against `HEAD`; both refuse on a hit with the law's remedy line.
7. `ratchet test` proves the law with a fixture whose `hit/base` holds a test the `hit/tip` lacks, and a `clean/` pair where the test moved files; `expected.txt` lists `a_test.go:TestFoo`.
8. `ratchet presets` lists `common/test_removed`, `go/test_removed`, `rust/test_removed`; the Rust pattern matches `#[test]` and `#[tokio::test]` including `async fn`.
9. This repo adopts `go/test_removed` in `.ratchet/laws/test_removed.toml`, severity deny, and its fixtures pass.
