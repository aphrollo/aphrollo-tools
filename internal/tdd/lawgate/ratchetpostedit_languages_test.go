package lawgate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// languageTestsTree is a committed repo with one symbol-removed law that
// states no pattern: what a test is comes from the language rows its scope
// names. t/t.py holds two Python tests and t/T.java two Java ones.
func languageTestsTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "removed_tests.toml"), `
name = "removed_tests"
description = "A test gone from the tree needs a tombstone"
severity = "deny"

[scope]
include = ["**/*.py", "**/*.java"]

[matcher]
kind = "symbol-removed"
`)
	mustWrite(t, filepath.Join(root, "t", "t.py"), "def test_one():\n    pass\n\ndef test_two():\n    pass\n")
	mustWrite(t, filepath.Join(root, "t", "T.java"), "@Test\nvoid one() {}\n\n@Test\nvoid two() {}\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

func TestEditLawRefusals_AnOmittedPatternNamesAPythonTestTheEditRemoved(t *testing.T) {
	root := languageTestsTree(t)
	mustWrite(t, filepath.Join(root, "t", "t.py"), "def test_one():\n    pass\n")

	got := strings.Join(editLawRefusals(root, []string{"t/t.py"}), "\n")

	if !strings.Contains(got, "removed_tests: t/t.py") {
		t.Fatalf("refusals = %q, want the removed Python test named", got)
	}
}

func TestEditLawRefusals_AnOmittedPatternNamesAJavaTestTheEditRemoved(t *testing.T) {
	root := languageTestsTree(t)
	mustWrite(t, filepath.Join(root, "t", "T.java"), "@Test\nvoid one() {}\n")

	got := strings.Join(editLawRefusals(root, []string{"t/T.java"}), "\n")

	if !strings.Contains(got, "removed_tests: t/T.java") {
		t.Fatalf("refusals = %q, want the removed Java test named", got)
	}
}

func TestEditLawRefusals_AnOmittedPatternLeavesAnEditThatDropsNoTestAlone(t *testing.T) {
	root := languageTestsTree(t)
	mustWrite(t, filepath.Join(root, "t", "t.py"), "def test_one():\n    pass\n\ndef test_two():\n    pass\n\ndef helper():\n    pass\n")

	if got := editLawRefusals(root, []string{"t/t.py"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none: an added helper removes no test", got)
	}
}

func TestAnyCaptureDropped_ALawWhoseScopeNamesNoTestRowIsJudgedInFull(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "removed_tests.toml"), `
name = "removed_tests"
description = "x"
severity = "deny"

[scope]
include = ["**/*.xyz"]

[matcher]
kind = "symbol-removed"
`)
	mustWrite(t, filepath.Join(root, "a.xyz"), "x\n")
	gitAddAll(t, root)
	commitAll(t, root)
	laws, err := ratchet.LoadLaws(root)
	if err != nil || len(laws) != 1 {
		t.Fatalf("LoadLaws = %v, %v", laws, err)
	}
	// The law can capture nothing, so the hook cannot tell that no test was
	// dropped: it plans the full judging, where the engine names the defect.
	if !newEditJudge(root, []string{"a.xyz"}).anyCaptureDropped(laws[0]) {
		t.Error("a law whose patterns cannot be resolved was treated as untouched")
	}
}
