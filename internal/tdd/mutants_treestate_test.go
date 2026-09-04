package tdd

import (
	"strings"
	"testing"
)

// lsTree is git's own listing shape: mode, type, blob sha, tab, path.
// (PlanDiffFiles call sites here classify with repoRoot "" — the tests carry
// no files whose kind depends on it, so that is bare ClassifyFile.)
func lsTree(entries ...string) string {
	var b strings.Builder
	for _, e := range entries {
		sha, path, _ := strings.Cut(e, " ")
		b.WriteString("100644 blob " + sha + "\t" + path + "\n")
	}
	return b.String()
}

// The state a run is judged against comes from the tree itself: one blob per
// file, one package per file (the nearest manifest above it), and one fence
// per package.
func TestTreeStateFromListing_ReadsABlobAndAPackageForEveryFile(t *testing.T) {
	st := treeStateWithDeps(lsTree(
		"m0 Cargo.toml",
		"a0 crates/a/Cargo.toml",
		"a1 crates/a/src/lib.rs",
		"a2 crates/a/tests/behaviour.rs",
		"b0 crates/b/Cargo.toml",
		"b1 crates/b/src/lib.rs",
		"d0 docs/design.md",
	), nil)

	if got := st.Blobs["crates/a/src/lib.rs"]; got != "a1" {
		t.Fatalf("blob = %q, want a1", got)
	}
	if got := st.Packages["crates/a/src/lib.rs"]; got != "crates/a" {
		t.Fatalf("package = %q, want the nearest manifest dir", got)
	}
	if got := st.Packages["docs/design.md"]; got != "" {
		t.Fatalf("package = %q, want the repo root for a file under no crate", got)
	}
	if st.Fences["crates/a"] == "" {
		t.Fatal("a package with source and test files must fence to something")
	}
	if st.Fences["crates/a"] == st.Fences["crates/b"] {
		t.Fatal("two packages with different files must not fence alike")
	}
}

// This test previously asserted the opposite of its second half: that a SOURCE
// edit must not move the hash, because the hash covered test blobs only. That
// rule let a mutant in a.rs keep a "caught" verdict after the sibling code
// that caught it changed, so the fence now moves for either edit.
func TestFence_MovesForASourceEditAndForATestEdit(t *testing.T) {
	base := lsTree("a0 crates/a/Cargo.toml", "a1 crates/a/src/lib.rs", "a2 crates/a/tests/behaviour.rs")
	srcMoved := lsTree("a0 crates/a/Cargo.toml", "a1-NEW crates/a/src/lib.rs", "a2 crates/a/tests/behaviour.rs")
	testMoved := lsTree("a0 crates/a/Cargo.toml", "a1 crates/a/src/lib.rs", "a2-NEW crates/a/tests/behaviour.rs")

	fence := func(listing string) string { return treeStateWithDeps(listing, nil).Fences["crates/a"] }
	if fence(base) == fence(srcMoved) {
		t.Fatal("a source edit left the fence unmoved: a mutant caught through that file would carry its old verdict")
	}
	if fence(base) == fence(testMoved) {
		t.Fatal("a test edit left the fence unmoved")
	}
	// Stable for one tree: the listing order must not decide the hash, or
	// every run would invalidate every package.
	shuffled := lsTree("a2 crates/a/tests/behaviour.rs", "a1 crates/a/src/lib.rs", "a0 crates/a/Cargo.toml")
	if fence(base) != fence(shuffled) {
		t.Fatal("the fence moved when only the listing order did")
	}
}

// The run is scoped by FILE, because that is what cargo-mutants' --in-diff
// takes: a file whose blob and whose package's test set are both unchanged
// since the previous receipt is left out of the diff entirely.
func TestPlanDiffFiles_LeavesOutAFileNothingChangedAround(t *testing.T) {
	now := TreeState{
		Blobs:    map[string]string{"crates/a/src/lib.rs": "a1", "crates/b/src/lib.rs": "b1"},
		Packages: map[string]string{"crates/a/src/lib.rs": "crates/a", "crates/b/src/lib.rs": "crates/b"},
		Fences:   map[string]string{"crates/a": "tsA", "crates/b": "tsB"},
	}
	prev := cachedOutcomes([]MutantOutcome{
		{File: "crates/a/src/lib.rs", Line: 1, Mutation: "m", Package: "crates/a", Blob: "a1", Fence: "tsA"},
		{File: "crates/b/src/lib.rs", Line: 1, Mutation: "m", Package: "crates/b", Blob: "b0-OLD", Fence: "tsB"},
	})
	got := PlanDiffFiles("", []string{"crates/a/src/lib.rs", "crates/b/src/lib.rs", "README.md"}, now, prev)
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
		Fences:   map[string]string{"crates/a": "tsA-NEW"},
	}
	prev := cachedOutcomes([]MutantOutcome{
		{File: "crates/a/src/lib.rs", Line: 1, Mutation: "m", Package: "crates/a", Blob: "a1", Fence: "tsA"},
		{File: "crates/a/tests/x.rs", Line: 1, Mutation: "m", Package: "crates/a", Blob: "t1", Fence: "tsA"},
	})
	got := PlanDiffFiles("", []string{"crates/a/src/lib.rs", "crates/a/tests/x.rs"}, now, prev)
	if len(got) != 2 {
		t.Fatalf("PlanDiffFiles = %v, want the whole package back in the run", got)
	}
}

// With no previous receipt every mutable file is in the run, and the files no
// mutant can live in never are.
func TestPlanDiffFiles_TakesEveryMutableFileOnAFirstRun(t *testing.T) {
	now := TreeState{Blobs: map[string]string{"src/lib.rs": "a1"}}
	got := PlanDiffFiles("", []string{"src/lib.rs", "README.md", ".github/workflows/ci.yml"}, now, nil)
	if len(got) != 1 || got[0] != "src/lib.rs" {
		t.Fatalf("PlanDiffFiles = %v, want just the source file", got)
	}
}
