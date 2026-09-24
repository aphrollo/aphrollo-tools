package gc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeGoTmpChild writes one file inside path, aged to idle, mirroring
// makeTargetDir (gc_straytarget_test.go): dirNewestAndSize judges idleness by
// the newest FILE inside a candidate, never the directory's own mtime, so a
// child with no file at all would read as newest.IsZero() and never qualify.
func makeGoTmpChild(t *testing.T, path string, idle time.Duration) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-idle)
	f := filepath.Join(path, "loose.go")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f, when, when); err != nil {
		t.Fatal(err)
	}
}

// TestGCGoTmp_ProposesAStaleChild pins the defect this category fixes: a
// `go test` run killed by a timeout, a panic, or the build-slot reaper never
// runs its own cleanup, so its directory under GoTmpRootDir stays — measured
// on one box at 4.6 GB across 1,331 entries, 803 of them untouched for over
// three days, with `gate gc` reporting "nothing reclaimable" against all of
// it.
func TestGCGoTmp_ProposesAStaleChild(t *testing.T) {
	repo := makeGoRepo(t)
	root := GoTmpRootDir(repo)
	if root == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", repo)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	stale := filepath.Join(root, "TestSomethingKilled001")
	makeGoTmpChild(t, stale, 4*24*time.Hour)

	got := gcGoTmpLitter(repo, DefaultGCAge, time.Now())
	if len(got) != 1 || got[0].Path != stale {
		t.Fatalf("candidates = %v, want only %s", paths(got), stale)
	}
	if got[0].Kind != GCKindGoTmp || got[0].Size == 0 {
		t.Errorf("candidate = %+v", got[0])
	}
}

// TestGCGoTmp_SparesAFreshChild guards the age gate: a temp dir a running
// `go test` is still writing into is minutes old, never days, because
// goTmpEnv (gotmpdir.go) hands each suite run a fresh directory rather than
// reusing one — so a fresh child must never be proposed regardless of what
// else sits beside it.
func TestGCGoTmp_SparesAFreshChild(t *testing.T) {
	repo := makeGoRepo(t)
	root := GoTmpRootDir(repo)
	if root == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", repo)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	fresh := filepath.Join(root, "TestStillRunning001")
	makeGoTmpChild(t, fresh, time.Minute)

	if got := gcGoTmpLitter(repo, DefaultGCAge, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %v, want none — the child is minutes old", paths(got))
	}
}

// TestGCGoTmp_NeverProposesMutantsDirEvenWhenStale pins the exclusion by
// name: .mutants sits beside the test-litter entries under the same gotmp
// root (mutantsCopyDirs, gc_mutantsdirs.go) but is the mutation runner's OWN
// working area, not test litter — gcMutantsRunDirs / gcMutantsTrees already
// own its lifecycle, and proposing it here too would race a live run.
func TestGCGoTmp_NeverProposesMutantsDirEvenWhenStale(t *testing.T) {
	repo := makeGoRepo(t)
	root := GoTmpRootDir(repo)
	if root == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", repo)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	mutants := filepath.Join(root, ".mutants")
	makeGoTmpChild(t, filepath.Join(mutants, "gate-prmerge-1"), 4*24*time.Hour)

	for _, c := range gcGoTmpLitter(repo, DefaultGCAge, time.Now()) {
		if c.Path == mutants {
			t.Fatalf("proposed .mutants/ — that is the mutation runner's own area, not test litter")
		}
	}
}

// TestGCGoTmp_NeverProposesTheRootItself pins the unit of this category: a
// stale ENTRY under gotmp, never gotmp itself — the root is a live directory
// every suite run recreates, and proposing it means the next `go test`
// recreates it out from under a sweep that is mid-delete.
func TestGCGoTmp_NeverProposesTheRootItself(t *testing.T) {
	repo := makeGoRepo(t)
	root := GoTmpRootDir(repo)
	if root == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", repo)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	stale := filepath.Join(root, "TestSomethingKilled001")
	makeGoTmpChild(t, stale, 4*24*time.Hour)

	for _, c := range gcGoTmpLitter(repo, DefaultGCAge, time.Now()) {
		if c.Path == root {
			t.Fatal("proposed gotmp itself, not just a child of it")
		}
	}
}

// TestGCGoTmp_MissingDirScansClean pins the refusal shape GoTmpRootDir
// already documents (gotmpdir_outside_test.go, issue #532): a repo outside
// any git checkout, or a gotmp root that was never created, must scan clean
// rather than error or panic.
func TestGCGoTmp_MissingDirScansClean(t *testing.T) {
	outside := t.TempDir()
	if got := gcGoTmpLitter(outside, DefaultGCAge, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %v, want none for a repo outside any git checkout", paths(got))
	}

	repo := makeGoRepo(t)
	root := GoTmpRootDir(repo)
	if root == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", repo)
	}
	_ = os.RemoveAll(root) // never created / already swept
	if got := gcGoTmpLitter(repo, DefaultGCAge, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %v, want none for a gotmp root that does not exist", paths(got))
	}
}

// TestAllGCScopes_IncludesGoTmpLitter pins the wiring: the manual sweep must
// consider gotmp litter, or `aphrollo gate gc` never sees the 4.6 GB this
// category exists to reclaim.
func TestAllGCScopes_IncludesGoTmpLitter(t *testing.T) {
	if !AllGCScopes().GateDirs {
		t.Fatal("setup: the manual sweep must run the GateDirs scope gotmp litter is wired under")
	}
	repo := makeGoRepo(t)
	root := GoTmpRootDir(repo)
	if root == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", repo)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	stale := filepath.Join(root, "TestSomethingKilled001")
	makeGoTmpChild(t, stale, 4*24*time.Hour)

	found := false
	for _, c := range ScanGC(repo, DefaultGCAge, AllGCScopes()) {
		if c.Path == stale {
			found = true
		}
	}
	if !found {
		t.Fatalf("ScanGC under AllGCScopes() did not propose %s", stale)
	}
}
