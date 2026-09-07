package tdd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mutantsBaselineScopeFixture writes a two-member cargo workspace
// (crates/alpha, crates/beta) under a fresh temp dir and returns its root —
// no git needed, since mutantsTouchedPackages reads the lane diff back from
// a plain file and walks Cargo.toml on disk.
func mutantsBaselineScopeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "crates/beta/Cargo.toml", "[package]\nname = \"beta\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	return root
}

func writeLaneDiff(t *testing.T, dir, rel string, touchedFiles ...string) string {
	t.Helper()
	var b []byte
	for _, f := range touchedFiles {
		b = append(b, []byte("diff --git a/"+f+" b/"+f+"\n--- a/"+f+"\n+++ b/"+f+"\n@@ -1 +1 @@\n-old\n+new\n")...)
	}
	p := filepath.Join(dir, rel)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A lane that only touched crates/alpha must scope to alpha alone: beta owns
// no file in the diff, so it must never appear in the touched-package set —
// the set --package later carries into cargo-mutants' baseline, which is
// what keeps beta's own tests out of the run entirely.
func TestMutantsTouchedPackages_NamesOnlyThePackageOwningTheDiffFile(t *testing.T) {
	t.Parallel()
	root := mutantsBaselineScopeFixture(t)
	diff := writeLaneDiff(t, root, "lane.diff", "crates/alpha/src/lib.rs")
	j := MutantsJob{RepoRoot: root, Diff: diff}

	got := mutantsTouchedPackages(j)
	want := []string{"alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutantsTouchedPackages = %v, want %v (beta must not appear)", got, want)
	}
}

// A diff touching files in both crates names both packages, sorted and
// deduped — the same shape cargoPackagesOwning already guarantees for the
// commit gate's own touched-crates stage.
func TestMutantsTouchedPackages_NamesEveryPackageADiffFileTouches(t *testing.T) {
	t.Parallel()
	root := mutantsBaselineScopeFixture(t)
	diff := writeLaneDiff(t, root, "lane.diff", "crates/beta/src/lib.rs", "crates/alpha/src/lib.rs")
	j := MutantsJob{RepoRoot: root, Diff: diff}

	got := mutantsTouchedPackages(j)
	want := []string{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutantsTouchedPackages = %v, want %v", got, want)
	}
}

// A diff this run cannot read back leaves the touched-crate set
// UNDETERMINABLE: nil, never an empty-but-meaningful answer, so the caller's
// only sound move is the current whole-workspace behaviour.
func TestMutantsTouchedPackages_NilOnUnreadableDiff(t *testing.T) {
	t.Parallel()
	root := mutantsBaselineScopeFixture(t)
	j := MutantsJob{RepoRoot: root, Diff: filepath.Join(root, "does-not-exist.diff")}

	if got := mutantsTouchedPackages(j); got != nil {
		t.Fatalf("mutantsTouchedPackages = %v, want nil for an unreadable diff", got)
	}
}

// A diff whose only touched file no cargo package owns (a virtual workspace
// manifest, or a path outside any member) is exactly as undeterminable as a
// missing diff: nil, not an empty scope silently measuring nothing.
func TestMutantsTouchedPackages_NilWhenNoTouchedFileIsCargoOwned(t *testing.T) {
	t.Parallel()
	root := mutantsBaselineScopeFixture(t)
	diff := writeLaneDiff(t, root, "lane.diff", "README.md")
	j := MutantsJob{RepoRoot: root, Diff: diff}

	if got := mutantsTouchedPackages(j); got != nil {
		t.Fatalf("mutantsTouchedPackages = %v, want nil when nothing touched is cargo-owned", got)
	}
}
