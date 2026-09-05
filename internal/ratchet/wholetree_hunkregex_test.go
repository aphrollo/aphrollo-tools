package ratchet

import (
	"path/filepath"
	"testing"
)

func hunkRegexRepo(t *testing.T, lawName, lawBody string) string {
	t.Helper()
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, lawName, lawBody)
	return root
}

const removedTestLaw = `
name = "removed_test_declaration"
description = "a Go test removed with no re-declaration of the same name"
severity = "deny"

[scope]
changed = "staged"
include = ["**/*_test.go"]

[matcher]
kind = "hunk-regex"
removed = "^func (Test[A-Za-z0-9_]+)\\("
name_group = true
`

// TestHunkRegex_NameGroupFlagsARemovedTestWithNoReAdd is #318's test_removed
// rule, built on hunk-regex's name_group mode rather than duplicating
// symbol-removed: a Go test line removed from a staged file's diff with no
// added declaration of the same name anywhere in that diff.
func TestHunkRegex_NameGroupFlagsARemovedTestWithNoReAdd(t *testing.T) {
	root := hunkRegexRepo(t, "removed_test_declaration", removedTestLaw)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", "a_test.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a_test.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Fatalf("findings = %+v, want one keyed a_test.go:TestFoo", res.Findings)
	}
}

// TestHunkRegex_NameGroupFlagsARenameToADifferentName proves the identity is
// the exact captured NAME, not "a test declaration remains somewhere": a
// rename to TestFooBar is a different name, so TestFoo still reads as
// removed — matching symbol-removed's own precedent for a rename.
func TestHunkRegex_NameGroupFlagsARenameToADifferentName(t *testing.T) {
	root := hunkRegexRepo(t, "removed_test_declaration", removedTestLaw)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFooBar(t *testing.T) {}\n")
	gitRun(t, root, "add", "a_test.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a_test.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Fatalf("findings = %+v, want one keyed a_test.go:TestFoo", res.Findings)
	}
}

// TestHunkRegex_NameGroupIgnoresTheSameNameReappearingElsewhere proves the
// "no added declaration of the SAME name" half literally: TestFoo is deleted
// from the top of the file and re-added, unchanged, at the bottom — the same
// diff carries both an added and a removed TestFoo line, so it is a move,
// not a loss.
func TestHunkRegex_NameGroupIgnoresTheSameNameReappearingElsewhere(t *testing.T) {
	root := hunkRegexRepo(t, "removed_test_declaration", removedTestLaw)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestBar(t *testing.T) {}\n\nfunc TestFoo(t *testing.T) {}\n")
	gitRun(t, root, "add", "a_test.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a_test.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — TestFoo moved, it was not lost", res.Findings)
	}
}

// TestHunkRegex_NameGroupEscapedByCommitTrailer proves the specified escape:
// a `Removes-test: <name>: <why>` commit-message trailer, because the
// removed line leaves no surviving position for an in-file comment.
func TestHunkRegex_NameGroupEscapedByCommitTrailer(t *testing.T) {
	root := hunkRegexRepo(t, "removed_test_declaration", removedTestLaw)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", "a_test.go")

	res, err := Check(Options{
		Root: root, Base: "HEAD", StagedFiles: []string{"a_test.go"},
		CommitMessage: "drop TestFoo\n\nRemoves-test: TestFoo: superseded by TestBar\n",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — the commit trailer admits TestFoo", res.Findings)
	}
}

const assertionWeakenedLaw = `
name = "assertion_weakened"
description = "an Equal assertion loosened to Contains in the same commit"
severity = "warn"

[scope]
changed = "staged"
include = ["**/*.go"]

[matcher]
kind = "hunk-regex"
removed = "assert\\.Equal\\("
added   = "assert\\.(Contains|NotNil)\\("
paired  = true
`

// TestHunkRegex_PairedForbidFlagsALoosenedAssertion is #318's
// assertion_weakened rule: a removed Equal line replaced, at the same
// position, by a Contains/NotNil line.
func TestHunkRegex_PairedForbidFlagsALoosenedAssertion(t *testing.T) {
	root := hunkRegexRepo(t, "assertion_weakened", assertionWeakenedLaw)
	write(t, filepath.Join(root, "x_test.go"), "package a\n\nfunc TestX(t *testing.T) {\n\tassert.Equal(t, 4, sum())\n}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "x_test.go"), "package a\n\nfunc TestX(t *testing.T) {\n\tassert.Contains(t, out, \"4\")\n}\n")
	gitRun(t, root, "add", "x_test.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"x_test.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Line != 4 {
		t.Fatalf("findings = %+v, want one at line 4 (the added line)", res.Findings)
	}
}

const expectationMovedLaw = `
name = "expectation_moved"
description = "a golden literal changed in the same commit as the source it checks"
severity = "warn"
escape = "// expectation-changed:"

[scope]
changed = "staged"
include = ["**/*.go"]

[matcher]
kind = "hunk-regex"
removed = "want := \"([^\"]*)\""
paired  = true
mode    = "differs"
`

// TestHunkRegex_DiffersFlagsAMovedLiteral is #318's expectation_moved rule: a
// removed and an added assertion line that differ only in the expected
// literal — same shape, a different value.
func TestHunkRegex_DiffersFlagsAMovedLiteral(t *testing.T) {
	root := hunkRegexRepo(t, "expectation_moved", expectationMovedLaw)
	write(t, filepath.Join(root, "y_test.go"), "package a\n\nfunc TestY(t *testing.T) {\n\twant := \"4\"\n\t_ = want\n}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "y_test.go"), "package a\n\nfunc TestY(t *testing.T) {\n\twant := \"5\"\n\t_ = want\n}\n")
	gitRun(t, root, "add", "y_test.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"y_test.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Line != 4 {
		t.Fatalf("findings = %+v, want one at line 4", res.Findings)
	}
}

// TestRunFixtures_ProvesAHunkRegexLawThroughBaseTip proves the fixture
// harness itself: `ratchet test` judges a hunk-regex law's `hit`/`clean`
// cases through the same `base/`+`tip/` layout symbol-removed already uses,
// with no git repo needed.
func TestRunFixtures_ProvesAHunkRegexLawThroughBaseTip(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "removed_test_declaration", removedTestLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "removed_test_declaration")
	write(t, filepath.Join(fx, "hit", "base", "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	write(t, filepath.Join(fx, "hit", "tip", "a_test.go"), "package a\n")
	write(t, filepath.Join(fx, "expected.txt"), "a_test.go:TestFoo\n")
	write(t, filepath.Join(fx, "clean", "base", "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	write(t, filepath.Join(fx, "clean", "tip", "a_test.go"), "package a\n\nfunc TestBar(t *testing.T) {}\n\nfunc TestFoo(t *testing.T) {}\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}

// TestHunkRegex_DiffersEscapedByExpectationChangedComment proves the
// specified escape: `// expectation-changed: <why>` on the added line — a
// standard contiguous escape, since the added line still exists at tip.
func TestHunkRegex_DiffersEscapedByExpectationChangedComment(t *testing.T) {
	root := hunkRegexRepo(t, "expectation_moved", expectationMovedLaw)
	write(t, filepath.Join(root, "y_test.go"), "package a\n\nfunc TestY(t *testing.T) {\n\twant := \"4\"\n\t_ = want\n}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "y_test.go"),
		"package a\n\nfunc TestY(t *testing.T) {\n\twant := \"5\" // expectation-changed: server now rounds up\n\t_ = want\n}\n")
	gitRun(t, root, "add", "y_test.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"y_test.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — the added line carries the escape", res.Findings)
	}
}
