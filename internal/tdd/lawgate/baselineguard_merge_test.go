package lawgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trussBefore and trussAfter are the same 4-line module either side of an
// edit trunk made while the lane was away. A lane hundreds of commits behind
// never finds trunk's copy of a moved file byte-identical to the one it
// branched from, so the content-pairing re-path rescue inside raisedKeys is
// deliberately NOT what decides these two tests: what decides them is
// whether the guard consults the merge's second parent at all.
const (
	trussBefore = "fn truss() {\n    let a = 1;\n    let b = 2;\n}\n"
	trussAfter  = "fn truss() {\n    let a = 1;\n    let b = 3;\n}\n"
)

const mergeBaselineRel = ".ratchet/baselines/module_size.txt"

// mergeRepathFixture builds a real two-branch repository and leaves it mid
// merge: trunk RE-PATHED a module (`tests/kernel/truss.rs` ->
// `tests/integration/kernel/truss.rs`) and the ratchet re-pathed its baseline
// row with it, the lane did unrelated work, and the lane then merged trunk in
// with `--no-commit` — the exact state the commit gate judges, MERGE_HEAD
// present and the merge result fully staged. Against the lane's HEAD alone
// the moved row is a brand-new key at 710-lines-from-nothing; against the
// merge's other parent it is what trunk itself already says.
func mergeRepathFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "crates", "x", "tests", "kernel", "truss.rs"), trussBefore)
	mustWrite(t, filepath.Join(root, filepath.FromSlash(mergeBaselineRel)),
		"# module size\ncrates/x/tests/kernel/truss.rs | 4\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitFixture(t, root, "branch", "-M", "main")

	// The lane branches off and does its own work, never touching a baseline.
	gitFixture(t, root, "checkout", "-q", "-b", "lane")
	mustWrite(t, filepath.Join(root, "crates", "x", "src", "lib.rs"), "pub fn x() {}\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// Trunk moves the module and edits it; its baseline row moves with it.
	gitFixture(t, root, "checkout", "-q", "main")
	if err := os.Remove(filepath.Join(root, "crates", "x", "tests", "kernel", "truss.rs")); err != nil {
		t.Fatalf("setup: removing the pre-move module: %v", err)
	}
	mustWrite(t, filepath.Join(root, "crates", "x", "tests", "integration", "kernel", "truss.rs"), trussAfter)
	mustWrite(t, filepath.Join(root, filepath.FromSlash(mergeBaselineRel)),
		"# module size\ncrates/x/tests/integration/kernel/truss.rs | 4\n")
	gitAddAll(t, root)
	commitAll(t, root)

	gitFixture(t, root, "checkout", "-q", "lane")
	gitFixture(t, root, "merge", "main", "--no-ff", "--no-commit", "-m", "merge main into lane")
	if _, err := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("setup: expected MERGE_HEAD after a real merge, stat err = %v", err)
	}
	return root
}

// TestBaselineGuard_AllowsARowTheMergedParentAlreadyCarries is issue #657: a
// lane merging trunk in stages the row TRUNK wrote, and the guard — which
// compared the staged baselines against HEAD alone — read a pure re-path as a
// ceiling raised from nothing and refused the merge. Nobody raised anything;
// the row is what one of the merge's own parents already says.
func TestBaselineGuard_AllowsARowTheMergedParentAlreadyCarries(t *testing.T) {
	root := mergeRepathFixture(t)

	res := baselineStage("premerge", root)
	if res.Blocked {
		t.Fatalf("a staged row the merged-in parent already carries is not a hand raise: %s", res.Message)
	}
}

// TestBaselineGuard_RefusesARowHigherThanBothMergeParents is the same fixture
// with the merged baseline then hand-raised past what EITHER parent says. A
// merge in progress is not a licence: this is the rule the guard exists for,
// and it must hold mid-merge exactly as it holds anywhere else.
func TestBaselineGuard_RefusesARowHigherThanBothMergeParents(t *testing.T) {
	root := mergeRepathFixture(t)
	mustWrite(t, filepath.Join(root, filepath.FromSlash(mergeBaselineRel)),
		"# module size\ncrates/x/tests/integration/kernel/truss.rs | 9\n")
	gitAddAll(t, root)

	res := baselineStage("premerge", root)
	if !res.Blocked {
		t.Fatal("a row above BOTH merge parents is a hand-raised ceiling and must still be refused mid-merge")
	}
	if !strings.Contains(res.Message, "4 -> 9") {
		t.Errorf("message must name the raise against the highest parent (4 -> 9): %s", res.Message)
	}
}
