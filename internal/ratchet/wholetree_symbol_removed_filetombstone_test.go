package ratchet

import (
	"os"
	"path/filepath"
	"testing"
)

// A test file deleted WITH its subject used to cost one tombstone per test
// function in it: #574 measured 318 of the 369 tombstone lines one lane wrote
// as naming a test whose whole file went away. These four prove the file-scope
// tombstone that replaces those 318 lines with 62, and — the point of the
// three refusals — that it admits nothing a per-test tombstone would have had
// to admit.

// TestSymbolRemoved_FileTombstoneAdmitsAWhollyRetiredTestFile is the reduction
// itself: one tombstone naming the deleted PATH admits every test that stood
// in it, once nothing from that file survives anywhere at tip.
func TestSymbolRemoved_FileTombstoneAdmitsAWhollyRetiredTestFile(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	write(t, filepath.Join(root, "b_test.go"), "package a\n\nfunc TestKeep(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	if err := os.Remove(filepath.Join(root, "a_test.go")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "b_test.go"),
		"package a\n\n// ratchet: test_removed a_test.go: the receipt machinery every test in it exercised was deleted with it\nfunc TestKeep(t *testing.T) {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — one tombstone naming the retired file admits every test that stood in it", res.Findings)
	}
}

// TestSymbolRemoved_FileTombstoneIsRefusedWhenTheFileSurvives is the hole this
// must not open: a file tombstone claims a whole file went away, so a file
// still standing at tip admits nothing at all — deleting the one failing test
// out of a surviving file still needs a tombstone naming THAT test.
func TestSymbolRemoved_FileTombstoneIsRefusedWhenTheFileSurvives(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"),
		"package a\n\n// ratchet: test_removed a_test.go: retired with its subject\nfunc TestBar(t *testing.T) {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one — a_test.go is still there, so the file tombstone's claim is false", res.Findings)
	}
	if res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Errorf("key = %q, want %q", res.Findings[0].Key, "a_test.go:TestFoo")
	}
}

// TestSymbolRemoved_FileTombstoneIsRefusedWhenPartOfTheFileMoved is the second
// refusal: a file whose tests moved elsewhere was SPLIT, not retired, and one
// test dropped on the way out is exactly the removal this law exists to
// report. Nothing at tip carries TestFoo's claim, so it still needs its own
// tombstone even though a_test.go itself is gone.
func TestSymbolRemoved_FileTombstoneIsRefusedWhenPartOfTheFileMoved(t *testing.T) {
	root := symbolRemovedRepo(t)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	if err := os.Remove(filepath.Join(root, "a_test.go")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "b_test.go"),
		"package a\n\n// ratchet: test_removed a_test.go: retired with its subject\nfunc TestBar(t *testing.T) {}\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one — TestBar moved to b_test.go, so a_test.go was split, not retired", res.Findings)
	}
	if res.Findings[0].Key != "a_test.go:TestFoo" {
		t.Errorf("key = %q, want %q", res.Findings[0].Key, "a_test.go:TestFoo")
	}
}

// fileTombstoneFixtureRepo lays the file-scope tombstone out as a repo's own
// tracked fixtures would: `hit/` is the surviving file whose tombstone admits
// nothing, `clean/` is the retired file whose one tombstone admits both its
// tests. Synthetic rather than tracked under .ratchet/fixtures, because this
// repo's commit gate proves tracked fixtures with the INSTALLED binary, which
// predates the behaviour below (see .ratchet/README.md, "Landing a new matcher
// field", for the same three-step sequence).
func fileTombstoneFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "test_removed", symbolRemovedLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "test_removed")

	write(t, filepath.Join(fx, "hit", "base", "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	write(t, filepath.Join(fx, "hit", "tip", "a_test.go"),
		"package a\n\n// ratchet: test_removed a_test.go: retired with its subject\nfunc TestBar(t *testing.T) {}\n")
	write(t, filepath.Join(fx, "expected.txt"), "a_test.go:TestFoo\n")

	write(t, filepath.Join(fx, "clean", "base", "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n\nfunc TestBar(t *testing.T) {}\n")
	write(t, filepath.Join(fx, "clean", "base", "b_test.go"), "package a\n\nfunc TestKeep(t *testing.T) {}\n")
	write(t, filepath.Join(fx, "clean", "tip", "b_test.go"),
		"package a\n\n// ratchet: test_removed a_test.go: the subject every test in it exercised was deleted with it\nfunc TestKeep(t *testing.T) {}\n")
	return root
}

// TestRunFixtures_ProvesTheFileTombstoneInBothDirections runs the pair through
// the same harness `aphrollo ratchet test` uses, so the rule is proved where a
// repo would prove it and not only through Check.
func TestRunFixtures_ProvesTheFileTombstoneInBothDirections(t *testing.T) {
	results, err := RunFixtures(fileTombstoneFixtureRepo(t))
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one law", results)
	}
	if len(results[0].Failures) != 0 {
		t.Fatalf("failures = %v, want none", results[0].Failures)
	}
}
