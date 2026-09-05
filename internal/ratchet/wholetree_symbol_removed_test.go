package ratchet

import (
	"path/filepath"
	"testing"
)

// symbolRemovedLaw is one inline `symbol-removed` law: `pattern` captures a
// Go test function's name in group 1, scoped to every `_test.go` file.
const symbolRemovedLaw = `
name = "test_removed"
description = "A test's disappearance from a diff needs a tombstone, not silence"
severity = "deny"

[scope]
include = ["**/*_test.go"]

[matcher]
kind = "symbol-removed"
pattern = "^func (Test[A-Za-z0-9_]+)\\("
`

// symbolRemovedRepo is a real git repo carrying one symbol-removed law and no
// baseline (this kind has no floor row — see check.go).
func symbolRemovedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "test_removed", symbolRemovedLaw)
	return root
}

func TestSymbolRemoved_ReportsATestPresentAtBaseAndGoneAtTip(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestBar(t *testing.T) {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", res.Findings)
	}
	if res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Errorf("key = %q, want %q", res.Findings[0].Key, "a_test.go:TestFoo")
	}
}

func TestSymbolRemoved_IgnoresAMoveByName(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestBar(t *testing.T) {}\n")
	write(t, filepath.Join(root, "b_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — TestFoo moved to b_test.go, it did not vanish", res.Findings)
	}
}

func TestSymbolRemoved_ReportsARenameAsTheOldName(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFooBar(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", res.Findings)
	}
	if res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Errorf("key = %q, want the OLD name %q", res.Findings[0].Key, "a_test.go:TestFoo")
	}
}

func TestSymbolRemoved_TombstoneWithReasonAdmitsTheRemoval(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"),
		"package a\n\n// ratchet: test_removed TestFoo: covered by TestFooTable since the table form landed\nfunc TestBar(t *testing.T) {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — the tombstone carries a reason", res.Findings)
	}

	// The variant with no reason after the colon must NOT admit the removal.
	write(t, filepath.Join(root, "a_test.go"),
		"package a\n\n// ratchet: test_removed TestFoo:\nfunc TestBar(t *testing.T) {}\n")

	res, err = Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one — a tombstone with no reason admits nothing", res.Findings)
	}
	if res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Errorf("key = %q, want %q", res.Findings[0].Key, "a_test.go:TestFoo")
	}
}

func TestSymbolRemoved_SkipsWithoutABaseAndSaysSo(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none without a base", res.Findings)
	}
	want := "test_removed: skipped, no base (pass --base <ref>)"
	found := false
	for _, n := range res.Notes {
		if n == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("Notes = %v, want %q", res.Notes, want)
	}
}

func TestSymbolRemoved_SkipsWhenTheBaseRefDoesNotResolve(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	// No commit: HEAD is unborn, so `git ls-tree -r --name-only HEAD` exits
	// 128 with `fatal: Not a valid object name HEAD` — the base ref itself
	// does not resolve, distinct from having no base at all.

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — the base ref does not resolve", res.Findings)
	}
	want := "test_removed: skipped, base HEAD not found (pass --base <ref>)"
	found := false
	for _, n := range res.Notes {
		if n == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("Notes = %v, want %q", res.Notes, want)
	}
}

// TestSymbolRemoved_MatchesADeclarationThatSpansTwoLines proves the engine
// evaluates a symbol-removed law's pattern against the whole file, not one
// physical line at a time: the Rust preset's `#[test]` attribute sits on its
// own line above the `fn` it marks, the idiomatic rustfmt layout, and a
// line-by-line scan never joins the two into one match.
func TestSymbolRemoved_MatchesADeclarationThatSpansTwoLines(t *testing.T) {
	rustRaw, err := LoadPresetText("rust", "test_removed")
	if err != nil {
		t.Fatalf("LoadPresetText(rust, test_removed): %v", err)
	}

	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "test_removed", rustRaw)

	write(t, filepath.Join(root, "a.rs"), "#[test]\nfn it_works() {}\n\n#[tokio::test]\nasync fn runs() {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.rs"), "#[tokio::test]\nasync fn runs() {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", res.Findings)
	}
	if res.Findings[0].Key != "a.rs:it_works" {
		t.Errorf("key = %q, want %q", res.Findings[0].Key, "a.rs:it_works")
	}
}

func TestSymbolRemoved_ReadsTheTipFromTheProposedOverlay(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")
	// The tree on disk still carries TestFoo — only the staged/proposed
	// content lacks it, exactly the shape of a staged-index commit gate.

	res, err := Check(Options{
		Root:     root,
		Base:     "HEAD",
		Proposed: map[string]string{"a_test.go": "package a\n"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one — the overlay, not the disk copy, is the tip", res.Findings)
	}
	if res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Errorf("key = %q, want %q", res.Findings[0].Key, "a_test.go:TestFoo")
	}
}
