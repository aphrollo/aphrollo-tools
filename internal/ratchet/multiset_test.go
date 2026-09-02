package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A line-keyed baseline is a MULTISET of offending TEXT. The path that happens
// to carry a line is not part of its identity: renaming or moving a file
// changes nothing about the debt, and a guard that reports a regression for
// `git mv` teaches everyone to hand-edit the baseline, which is the one thing
// it exists to prevent.
func TestCheckIsCleanAfterAFileWithBaselinedHitsMoves(t *testing.T) {
	root := repoWithNanGuard(t)
	if err := os.Remove(filepath.Join(root, "crates", "a", "src", "lib.rs")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "crates", "b", "src", "renamed.rs"), "let a = x.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a move is not a regression, findings = %+v", res.Findings)
	}
}

// The count is what the ceiling bounds, so paying one of two identical
// offences down lowers it — the ratchet turns on exactly this.
func TestCheckTightensWhenOneOfTwoIdenticalLinesIsFixed(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	baseline := filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")
	write(t, baseline, "crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\ncrates/b/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let a = 1;\n")

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	after, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(after), "clamp"); n != 1 {
		t.Fatalf("baseline keeps %d rows, want 1:\n%s", n, after)
	}
}

// The other direction: at the ceiling, one MORE occurrence of the same text
// is a regression wherever it lands. A per-file key could not see this — it
// would read a brand-new file's offence as a brand-new key at ceiling zero
// only, and never notice the workspace total rising.
func TestCheckReportsAnIdenticalLineAddedInAnotherFile(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", res.Findings)
	}
	f := res.Findings[0]
	if f.Baseline != 1 || f.Measured != 2 {
		t.Errorf("baseline %d, measured %d — want 1 and 2", f.Baseline, f.Measured)
	}
}

// The file stays readable: a row still says WHERE the debt is, and tightening
// refreshes that path from the sites the scan actually found, so a moved hit
// stops pointing at a file that is gone.
func TestTightenRewritesTheBaselinePathOfAMovedHit(t *testing.T) {
	root := repoWithNanGuard(t)
	if err := os.Remove(filepath.Join(root, "crates", "a", "src", "lib.rs")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "crates", "b", "src", "renamed.rs"), "let a = x.clamp(0.0, 1.0);\n")

	if _, err := Check(Options{Root: root, Tighten: true}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "crates/b/src/renamed.rs | let a = x.clamp(0.0, 1.0);") {
		t.Fatalf("the row must follow the hit to its new file:\n%s", after)
	}
	if strings.Contains(string(after), "crates/a/src/lib.rs") {
		t.Errorf("the old path must not survive:\n%s", after)
	}
	if !strings.Contains(string(after), "# one known site") {
		t.Errorf("header comments are preserved:\n%s", after)
	}
}

// Tightening now runs on every clean pass so a moved row can be re-pathed,
// which must not turn into "invent a baseline file for every law that has
// none" — a law at a bar of zero has nothing to record.
func TestTightenNeverCreatesAnEmptyBaselineFile(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = 1;\n")

	if _, err := Check(Options{Root: root, Tighten: true}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")); !os.IsNotExist(err) {
		t.Fatalf("a law with no debt must leave no baseline file behind (err = %v)", err)
	}
}

// A count-keyed law measures a PROPERTY OF A FILE (its length), so the file
// is the identity and moving it is a new key at ceiling zero. Collapsing
// those into a text multiset would let a 900-line module be renamed forever.
func TestCheckKeepsThePathKeyForACountedLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "big-file", `
name = "big-file"
description = "modules stay small"
severity = "deny"
baseline = ".ratchet/baselines/big-file.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "line-count"
max = 1
`)
	write(t, filepath.Join(root, ".ratchet", "baselines", "big-file.txt"), "crates/a/src/big.rs | 2\n")
	write(t, filepath.Join(root, "crates", "b", "src", "big.rs"), "let a = 1;\nlet b = 2;\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "crates/b/src/big.rs" {
		t.Fatalf("findings = %+v — a counted law is keyed by its file", res.Findings)
	}
}
