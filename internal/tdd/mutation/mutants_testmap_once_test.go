package mutation

import (
	"bytes"
	"strings"
	"testing"
)

// A test-map build is a compile and a solo run of every test of the
// repository: two at once only race each other to the same maps on a box
// that has no cores to spare (#1248). One builds; a request that arrives
// meanwhile is folded into one more pass of the build already running.

func TestTestMapOnce_ASecondRequestWhileOneBuildsStartsNoBuildOfItsOwn(t *testing.T) {
	root := t.TempDir()
	release, ok := TryAcquireFileLock(testMapLockPath(root))
	if !ok {
		t.Fatal("could not take the lock a running build holds")
	}
	defer release()

	builds := 0
	var out bytes.Buffer
	if code := testMapOnce(root, func() int { builds++; return 0 }, &out); code != 0 {
		t.Fatalf("exit = %d, want 0: a folded request is not a failure", code)
	}
	if builds != 0 {
		t.Fatalf("builds = %d while another build held the repository, want 0", builds)
	}
	if !strings.Contains(out.String(), "already running") {
		t.Errorf("output = %q, want it to say a build is already running", out.String())
	}
}

func TestTestMapOnce_ARequestDuringABuildMakesItRunExactlyOnceMore(t *testing.T) {
	root := t.TempDir()
	builds := 0
	var out bytes.Buffer
	testMapOnce(root, func() int {
		builds++
		if builds == 1 {
			// Two more merges land while the first pass runs; each asks.
			var asked bytes.Buffer
			testMapOnce(root, func() int { t.Error("a request built while the lock was held"); return 0 }, &asked)
			testMapOnce(root, func() int { t.Error("a request built while the lock was held"); return 0 }, &asked)
		}
		return 0
	}, &out)
	if builds != 2 {
		t.Errorf("builds = %d, want 2: the first pass, then one more for every request that came during it", builds)
	}
}

func TestTestMapOnce_AQuietRepositoryBuildsOnce(t *testing.T) {
	builds := 0
	var out bytes.Buffer
	testMapOnce(t.TempDir(), func() int { builds++; return 0 }, &out)
	if builds != 1 {
		t.Errorf("builds = %d, want 1", builds)
	}
}
