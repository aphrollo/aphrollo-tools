package ratchet

import (
	"os"
	"path/filepath"
	"testing"
)

// A JavaScript test runner names a test by a free-form string, so a
// symbol-removed law over it captures names with spaces, dots and slashes. The
// bare tombstone form cannot carry such a name; the quoted form can, and a
// quoted name is always one test, never a path.

// quotedNameLaw captures the string name of an `it(...)`/`test(...)` call.
const quotedNameLaw = `
name = "test_removed"
description = "A test's disappearance from a diff needs a tombstone, not silence"
severity = "deny"

[scope]
include = ["**/*.test.ts"]

[matcher]
kind = "symbol-removed"
pattern = "\\b(?:it|test)\\(\\s*['\"]([^'\"]+)['\"]"
`

func quotedNameRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "test_removed", quotedNameLaw)
	return root
}

// TestSymbolRemoved_QuotedTombstoneAdmitsATestNamedWithSpaces retires one test
// out of a file that survives, in both quote styles, and proves the tombstone
// admits exactly the test it names: a second removal stays reported.
func TestSymbolRemoved_QuotedTombstoneAdmitsATestNamedWithSpaces(t *testing.T) {
	root := quotedNameRepo(t)
	write(t, filepath.Join(root, "nav.test.ts"),
		"it('renders null as a dash', () => {})\nit(\"keeps a.b/c intact\", () => {})\nit('stays', () => {})\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "nav.test.ts"),
		"// ratchet: test_removed \"renders null as a dash\": the dash placeholder was removed with the feature\n"+
			"// ratchet: test_removed 'keeps a.b/c intact': the path helper it exercised is gone\n"+
			"it('stays', () => {})\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none: each quoted tombstone names its test and gives a reason", res.Findings)
	}

	write(t, filepath.Join(root, "nav.test.ts"),
		"// ratchet: test_removed \"renders null as a dash\": the dash placeholder was removed with the feature\n"+
			"it('stays', () => {})\n")

	res, err = Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "nav.test.ts:keeps a.b/c intact" {
		t.Fatalf("findings = %+v, want exactly the untombstoned test nav.test.ts:keeps a.b/c intact", res.Findings)
	}
}

// TestSymbolRemoved_QuotedTombstoneNeedsAReason: the quoted form keeps the
// bare form's rule, a stub with nothing after the colon admits nothing.
func TestSymbolRemoved_QuotedTombstoneNeedsAReason(t *testing.T) {
	root := quotedNameRepo(t)
	write(t, filepath.Join(root, "nav.test.ts"), "it('renders null as a dash', () => {})\nit('stays', () => {})\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "nav.test.ts"),
		"// ratchet: test_removed \"renders null as a dash\":\nit('stays', () => {})\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "nav.test.ts:renders null as a dash" {
		t.Fatalf("findings = %+v, want exactly nav.test.ts:renders null as a dash", res.Findings)
	}
}

// TestSymbolRemoved_AQuotedNameIsNeverAPath: a quoted tombstone naming a
// deleted file's path is read as a test name, so it does not admit the file's
// tests the way a bare path tombstone would.
func TestSymbolRemoved_AQuotedNameIsNeverAPath(t *testing.T) {
	root := quotedNameRepo(t)
	write(t, filepath.Join(root, "old.test.ts"), "it('retired', () => {})\n")
	write(t, filepath.Join(root, "keep.test.ts"), "it('stays', () => {})\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	if err := os.Remove(filepath.Join(root, "old.test.ts")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "keep.test.ts"),
		"// ratchet: test_removed \"old.test.ts\": the subject went with it\nit('stays', () => {})\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "old.test.ts:retired" {
		t.Fatalf("findings = %+v, want exactly old.test.ts:retired", res.Findings)
	}
}

// TestSymbolRemoved_BareTombstoneAdmitsATestNamedWithSpaces: a bare name runs
// up to the ': ' that starts the reason, so a Vitest name with spaces needs no
// quotes (issue #954). A name carrying a dot or slash is still one test, not a
// path, once it holds a space; a space before the colon is not part of the
// name; and a second removal stays reported.
func TestSymbolRemoved_BareTombstoneAdmitsATestNamedWithSpaces(t *testing.T) {
	root := quotedNameRepo(t)
	write(t, filepath.Join(root, "nav.test.ts"),
		"it('the Today page needs a legacy-tier team', () => {})\n"+
			"it('keeps a.b/c intact', () => {})\n"+
			"it('renders a dash', () => {})\n"+
			"it('gone without a word', () => {})\n"+
			"it('stays', () => {})\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "nav.test.ts"),
		"// ratchet: test_removed the Today page needs a legacy-tier team: the page it covered was deleted\n"+
			"// ratchet: test_removed keeps a.b/c intact: the path helper is gone: see the PR\n"+
			"// ratchet: test_removed renders a dash : the placeholder went with the feature\n"+
			"it('stays', () => {})\n")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "nav.test.ts:gone without a word" {
		t.Fatalf("findings = %+v, want exactly the untombstoned nav.test.ts:gone without a word", res.Findings)
	}
}
