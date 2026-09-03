package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mutantsCopy makes one cargo-mutants tree copy in dir, with a file in it so
// it has a size and an age.
func mutantsCopy(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(path, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(path, "src", "lib.rs")
	if err := os.WriteFile(f, []byte("pub fn a() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

// withCopyOwner points the ownership probe at a fixed answer.
func withCopyOwner(t *testing.T, owner func(string) (int, bool)) {
	t.Helper()
	prev := mutantsCopyOwnerFn
	mutantsCopyOwnerFn = owner
	t.Cleanup(func() { mutantsCopyOwnerFn = prev })
}

// cargo-mutants' default is a full tree COPY in the OS temp dir, and nothing
// ever collected them: 11 copies of one repo, ~135 MB each, were measured on
// one box. A copy whose run is over is pure garbage.
func TestGCMutantsTempCopies_ReclaimsACopyWhoseRunIsOver(t *testing.T) {
	tmp := t.TempDir()
	withCopyOwner(t, func(string) (int, bool) { return 0, false })
	path := mutantsCopy(t, tmp, "cargo-mutants-tire-drag-Vs5J5l.tmp", 2*time.Hour)

	got := gcMutantsTempCopies([]string{tmp}, time.Now())
	if len(got) != 1 || got[0].Path != path {
		t.Fatalf("candidates = %+v, want the abandoned copy %s", got, path)
	}
	if got[0].Kind != GCKindMutantsTemp {
		t.Fatalf("kind = %v, want the mutants-temp category", got[0].Kind)
	}
	if got[0].Size == 0 {
		t.Fatal("a candidate with no size tells an operator nothing")
	}
}

// A copy a run is still using is the tree it is testing right now. It is
// LISTED with the pid holding it, and never proposed.
func TestGCMutantsTempCopies_LeavesALiveCopyAndNamesItsPid(t *testing.T) {
	tmp := t.TempDir()
	live := mutantsCopy(t, tmp, "cargo-mutants-borld-aqMA8T.tmp", time.Minute)
	dead := mutantsCopy(t, tmp, "cargo-mutants-borld-Ke8zWa.tmp", 3*time.Hour)
	withCopyOwner(t, func(dir string) (int, bool) {
		if dir == live {
			return 4242, true
		}
		return 0, false
	})

	got := gcMutantsTempCopies([]string{tmp}, time.Now())
	if len(got) != 1 || got[0].Path != dead {
		t.Fatalf("candidates = %+v, want only the abandoned copy", got)
	}
	lines := MutantsCopiesInUse([]string{tmp})
	if len(lines) != 1 || !strings.Contains(lines[0], "4242") || !strings.Contains(lines[0], filepath.Base(live)) {
		t.Fatalf("in-use lines = %v, want one naming %s and pid 4242", lines, filepath.Base(live))
	}
}

// A probe that cannot answer means LIVE. Deleting a tree copy a run is
// mutating right now destroys hours of work; leaving one costs a listing.
func TestGCMutantsTempCopies_KeepsEverythingWhenOwnershipIsUnknowable(t *testing.T) {
	tmp := t.TempDir()
	mutantsCopy(t, tmp, "cargo-mutants-borld-Pb1F5R.tmp", 5*time.Hour)
	withCopyOwner(t, func(string) (int, bool) { return 0, true })

	if got := gcMutantsTempCopies([]string{tmp}, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %+v, want nothing proposed when ownership cannot be told", got)
	}
}

// The sweep only ever touches what it recognises. A directory that is not one
// of these copies is somebody's data.
func TestGCMutantsTempCopies_TouchesNothingElseInTheTempDir(t *testing.T) {
	tmp := t.TempDir()
	withCopyOwner(t, func(string) (int, bool) { return 0, false })
	mutantsCopy(t, tmp, "important-work", 5*time.Hour)
	mutantsCopy(t, tmp, "cargo-mutants", 5*time.Hour)

	if got := gcMutantsTempCopies([]string{tmp}, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %+v, want only cargo-mutants-<name> copies", got)
	}
}
