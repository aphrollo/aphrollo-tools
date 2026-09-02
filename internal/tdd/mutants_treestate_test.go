package tdd

import (
	"strings"
	"testing"
)

// lsTree is git's own listing shape: mode, type, blob sha, tab, path.
func lsTree(entries ...string) string {
	var b strings.Builder
	for _, e := range entries {
		sha, path, _ := strings.Cut(e, " ")
		b.WriteString("100644 blob " + sha + "\t" + path + "\n")
	}
	return b.String()
}

// The state a run is judged against comes from the tree itself: one blob per
// file, one package per file (the nearest manifest above it), and one hash per
// package over its TEST files' blobs.
func TestTreeStateFromListing_HashesEachPackagesTestFiles(t *testing.T) {
	st := treeStateFromListing(lsTree(
		"m0 Cargo.toml",
		"a0 crates/a/Cargo.toml",
		"a1 crates/a/src/lib.rs",
		"a2 crates/a/tests/behaviour.rs",
		"b0 crates/b/Cargo.toml",
		"b1 crates/b/src/lib.rs",
		"d0 docs/design.md",
	))

	if got := st.Blobs["crates/a/src/lib.rs"]; got != "a1" {
		t.Fatalf("blob = %q, want a1", got)
	}
	if got := st.Packages["crates/a/src/lib.rs"]; got != "crates/a" {
		t.Fatalf("package = %q, want the nearest manifest dir", got)
	}
	if got := st.Packages["docs/design.md"]; got != "" {
		t.Fatalf("package = %q, want the repo root for a file under no crate", got)
	}
	if st.TestSets["crates/a"] == "" {
		t.Fatal("a package with a test file must hash to something")
	}
	if st.TestSets["crates/a"] == st.TestSets["crates/b"] {
		t.Fatal("two packages with different test sets must not hash alike")
	}
}

// The hash is over the TEST files only, and it moves when one of them does:
// that is the whole signal that a package's mutants have to be re-measured.
func TestTreeStateFromListing_TestSetHashTracksOnlyTestBlobs(t *testing.T) {
	base := lsTree("a0 crates/a/Cargo.toml", "a1 crates/a/src/lib.rs", "a2 crates/a/tests/behaviour.rs")
	srcMoved := lsTree("a0 crates/a/Cargo.toml", "a1-NEW crates/a/src/lib.rs", "a2 crates/a/tests/behaviour.rs")
	testMoved := lsTree("a0 crates/a/Cargo.toml", "a1 crates/a/src/lib.rs", "a2-NEW crates/a/tests/behaviour.rs")

	if treeStateFromListing(base).TestSets["crates/a"] != treeStateFromListing(srcMoved).TestSets["crates/a"] {
		t.Fatal("a source edit must not move the test-set hash")
	}
	if treeStateFromListing(base).TestSets["crates/a"] == treeStateFromListing(testMoved).TestSets["crates/a"] {
		t.Fatal("a test edit must move the test-set hash")
	}
}

// The run is scoped by FILE, because that is what cargo-mutants' --in-diff
// takes: a file whose blob and whose package's test set are both unchanged
// since the previous receipt is left out of the diff entirely.
func TestPlanDiffFiles_LeavesOutAFileNothingChangedAround(t *testing.T) {
	now := TreeState{
		Blobs:    map[string]string{"crates/a/src/lib.rs": "a1", "crates/b/src/lib.rs": "b1"},
		Packages: map[string]string{"crates/a/src/lib.rs": "crates/a", "crates/b/src/lib.rs": "crates/b"},
		TestSets: map[string]string{"crates/a": "tsA", "crates/b": "tsB"},
	}
	prev := &MutationReceipt{
		Files:    map[string]string{"crates/a/src/lib.rs": "a1", "crates/b/src/lib.rs": "b0-OLD"},
		TestSets: map[string]string{"crates/a": "tsA", "crates/b": "tsB"},
	}
	got := PlanDiffFiles([]string{"crates/a/src/lib.rs", "crates/b/src/lib.rs", "README.md"}, now, prev)
	if len(got) != 1 || got[0] != "crates/b/src/lib.rs" {
		t.Fatalf("PlanDiffFiles = %v, want only the file whose blob moved", got)
	}
}

// A changed TEST file pulls its whole package back into the run: every mutant
// in it may now be caught by a test that did not exist last time.
func TestPlanDiffFiles_PullsInAWholePackageWhoseTestSetChanged(t *testing.T) {
	now := TreeState{
		Blobs:    map[string]string{"crates/a/src/lib.rs": "a1", "crates/a/tests/x.rs": "t2"},
		Packages: map[string]string{"crates/a/src/lib.rs": "crates/a", "crates/a/tests/x.rs": "crates/a"},
		TestSets: map[string]string{"crates/a": "tsA-NEW"},
	}
	prev := &MutationReceipt{
		Files:    map[string]string{"crates/a/src/lib.rs": "a1", "crates/a/tests/x.rs": "t1"},
		TestSets: map[string]string{"crates/a": "tsA"},
	}
	got := PlanDiffFiles([]string{"crates/a/src/lib.rs", "crates/a/tests/x.rs"}, now, prev)
	if len(got) != 2 {
		t.Fatalf("PlanDiffFiles = %v, want the whole package back in the run", got)
	}
}

// With no previous receipt every mutable file is in the run, and the files no
// mutant can live in never are.
func TestPlanDiffFiles_TakesEveryMutableFileOnAFirstRun(t *testing.T) {
	now := TreeState{Blobs: map[string]string{"src/lib.rs": "a1"}}
	got := PlanDiffFiles([]string{"src/lib.rs", "README.md", ".github/workflows/ci.yml"}, now, nil)
	if len(got) != 1 || got[0] != "src/lib.rs" {
		t.Fatalf("PlanDiffFiles = %v, want just the source file", got)
	}
}
