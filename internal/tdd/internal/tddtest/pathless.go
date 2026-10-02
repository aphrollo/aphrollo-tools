package tddtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verdictWordDir is the name prefix of the directory VerdictWordTmp puts a
// test's temp tree under: the words a gate verdict is made of, so a path
// carrying them is the rule in a test that uses it, not the accident of one
// box's checkout path.
const verdictWordDir = "green-red-"

// VerdictWordTmp points every directory the test later takes from t.TempDir at
// a parent whose path carries "green" and "red". A gate line names the tree it
// ran in, so an assertion that a line lacks a verdict word, judged over the
// whole line, fails in such a tree; with this call it fails on every run
// rather than only where the checkout path happens to hold the word. Call it
// before the test's first t.TempDir, or it refuses; the directories handed out
// are resolved through symlinks, so code that resolves a path prints the same
// text.
func VerdictWordTmp(t testing.TB) {
	t.Helper()
	dir, err := os.MkdirTemp(os.Getenv("GOTMPDIR"), verdictWordDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOTMPDIR", resolved)
	if !strings.HasPrefix(t.TempDir(), resolved+string(filepath.Separator)) {
		t.Fatal("VerdictWordTmp must run before the first t.TempDir, which fixes the parent of every later one")
	}
}

// Pathless returns out with the test's own temp tree, the parent of every
// t.TempDir directory, replaced by `<tmp>`, so an assertion on the result
// judges what the code printed and never the text of the path it ran under.
func Pathless(t testing.TB, out string) string {
	t.Helper()
	tree := filepath.Dir(t.TempDir())
	// A gate line spells one path both ways on Windows: as the OS does, and
	// with forward slashes in the command it suggests running.
	return strings.ReplaceAll(strings.ReplaceAll(out, tree, "<tmp>"), filepath.ToSlash(tree), "<tmp>")
}
