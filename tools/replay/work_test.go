package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ratchet: test_removed TestRemoveWork_TakesWhatTheReplayMadeAndNothingElse: removeWork is replaced by a claim of the paths the replay will create; TestWorkClaim_SettleRemovesOnlyTheClaimedPaths
// ratchet: test_removed TestRemoveWork_RemovesTheDirectoryItselfOnceEmpty: same replacement; TestWorkClaim_SettleRemovesTheDirectoryOnlyWhenTheReplayMadeIt
// ratchet: test_removed TestReplay_AGivenWorkDirIsCleanedWhenTheReplayFails: a failed replay now keeps its work directory; TestReplay_AFailedReplayKeepsItsWorkDirAndSaysWhere

func TestClaimWork_RefusesAWorkDirThatAlreadyHoldsAName(t *testing.T) {
	work := t.TempDir()
	writeWorkFile(t, filepath.Join(work, "previous-src", "go.mod"))

	_, err := claimWork(work, false)

	if err == nil || !strings.Contains(err.Error(), "previous-src") {
		t.Fatalf("err = %v, want the pre-existing name refused and named", err)
	}
	if _, statErr := os.Stat(filepath.Join(work, "previous-src", "go.mod")); statErr != nil {
		t.Errorf("a refused claim touched what was there: %v", statErr)
	}
}

func TestWorkClaim_SettleRemovesOnlyTheClaimedPaths(t *testing.T) {
	work := t.TempDir()
	writeWorkFile(t, filepath.Join(work, "notes", "mine.txt"))
	c, err := claimWork(work, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"area/s", "bin/candidate", "previous-src/go.mod", "self/tree/a.go", "synthetic/tree/b.ts", "self-store/c", "synthetic-store/d"} {
		writeWorkFile(t, filepath.Join(work, p))
	}

	c.settle(true, false, io.Discard)

	for _, d := range replayWorkDirs {
		if _, err := os.Stat(filepath.Join(work, d)); err == nil {
			t.Errorf("%s survived", d)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "notes", "mine.txt")); err != nil {
		t.Errorf("a file the replay did not make was removed: %v", err)
	}
	if _, err := os.Stat(work); err != nil {
		t.Errorf("a directory the replay did not make was removed: %v", err)
	}
}

func TestWorkClaim_SettleRemovesTheDirectoryOnlyWhenTheReplayMadeIt(t *testing.T) {
	work := filepath.Join(t.TempDir(), "replay-work")
	c, err := claimWork(work, true)
	if err != nil {
		t.Fatal(err)
	}
	writeWorkFile(t, filepath.Join(work, "previous-src", "go.mod"))

	c.settle(true, false, io.Discard)

	if _, err := os.Stat(work); err == nil {
		t.Error("an emptied work directory the replay made survived")
	}
}

func TestWorkClaim_AFailedOrKeptReplayLeavesEverythingAndNamesTheDirectory(t *testing.T) {
	for name, c := range map[string]struct{ passed, keep bool }{
		"failed": {false, false},
		"kept":   {true, true},
	} {
		work := filepath.Join(t.TempDir(), "replay-work")
		claim, err := claimWork(work, true)
		if err != nil {
			t.Fatal(err)
		}
		writeWorkFile(t, filepath.Join(work, "previous-src", "go.mod"))
		var out bytes.Buffer

		claim.settle(c.passed, c.keep, &out)

		if _, err := os.Stat(filepath.Join(work, "previous-src", "go.mod")); err != nil {
			t.Errorf("%s: the work directory was touched: %v", name, err)
		}
		if !strings.Contains(out.String(), work) {
			t.Errorf("%s: output %q does not say where the work directory is", name, out.String())
		}
	}
}

func TestReplay_AFailedReplayKeepsItsWorkDirAndSaysWhere(t *testing.T) {
	work := filepath.Join(t.TempDir(), "replay-work")
	missing := filepath.Join(t.TempDir(), "no-such-repo")
	var out bytes.Buffer

	_, err := replay(config{Repo: missing, Work: work, Files: 1, Lines: 1000}, &out)

	if err == nil {
		t.Fatal("a replay of a missing repository succeeded")
	}
	if _, statErr := os.Stat(filepath.Join(work, "bin")); statErr != nil {
		t.Errorf("the failed replay's work was removed: %v", statErr)
	}
	if !strings.Contains(out.String(), work) {
		t.Errorf("output %q does not say where the work directory is", out.String())
	}
}

func TestReplay_AWorkDirHoldingAFixedNameIsRefusedBeforeAnythingIsRemoved(t *testing.T) {
	work := t.TempDir()
	writeWorkFile(t, filepath.Join(work, "bin", "precious"))
	missing := filepath.Join(t.TempDir(), "no-such-repo")

	_, err := replay(config{Repo: missing, Work: work, HeadBin: "candidate", PrevBin: "previous", Files: 1, Lines: 1000}, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "bin") {
		t.Fatalf("err = %v, want the pre-existing bin refused", err)
	}
	if _, statErr := os.Stat(filepath.Join(work, "bin", "precious")); statErr != nil {
		t.Errorf("a path that pre-existed was removed: %v", statErr)
	}
}

func TestReplay_KeepLeavesTheWorkDirForInspection(t *testing.T) {
	work := filepath.Join(t.TempDir(), "replay-work")
	missing := filepath.Join(t.TempDir(), "no-such-repo")

	_, _ = replay(config{Repo: missing, Work: work, Keep: true, Files: 1, Lines: 1000}, io.Discard)

	if _, err := os.Stat(filepath.Join(work, "bin")); err != nil {
		t.Errorf("-keep removed the work directory: %v", err)
	}
}
