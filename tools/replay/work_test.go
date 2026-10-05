package main

import (
	"io"
	"os"
	"path/filepath"
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

func TestRemoveWork_TakesWhatTheReplayMadeAndNothingElse(t *testing.T) {
	work := t.TempDir()
	for _, p := range []string{"area/s", "bin/candidate", "previous-src/go.mod", "self/tree/a.go", "synthetic/tree/b.ts", "self-store/c", "synthetic-store/d"} {
		writeWorkFile(t, filepath.Join(work, p))
	}
	writeWorkFile(t, filepath.Join(work, "notes", "mine.txt"))

	removeWork(work)

	for _, d := range []string{"area", "bin", "previous-src", "self", "synthetic", "self-store", "synthetic-store"} {
		if _, err := os.Stat(filepath.Join(work, d)); err == nil {
			t.Errorf("%s survived", d)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "notes", "mine.txt")); err != nil {
		t.Errorf("a file the replay did not make was removed: %v", err)
	}
}

func TestRemoveWork_RemovesTheDirectoryItselfOnceEmpty(t *testing.T) {
	work := filepath.Join(t.TempDir(), "replay-work")
	writeWorkFile(t, filepath.Join(work, "previous-src", "go.mod"))

	removeWork(work)

	if _, err := os.Stat(work); err == nil {
		t.Error("an emptied work directory survived")
	}
}

func TestReplay_AGivenWorkDirIsCleanedWhenTheReplayFails(t *testing.T) {
	work := filepath.Join(t.TempDir(), "replay-work")
	writeWorkFile(t, filepath.Join(work, "previous-src", "go.mod"))
	missing := filepath.Join(t.TempDir(), "no-such-repo")

	_, err := replay(config{Repo: missing, Work: work, HeadBin: "candidate", PrevBin: "previous", Files: 1, Lines: 1000}, io.Discard)

	if err == nil {
		t.Fatal("a replay of a missing repository succeeded")
	}
	if _, statErr := os.Stat(work); statErr == nil {
		t.Error("the -work directory outlived the replay")
	}
}

func TestReplay_KeepLeavesTheWorkDirForInspection(t *testing.T) {
	work := filepath.Join(t.TempDir(), "replay-work")
	writeWorkFile(t, filepath.Join(work, "previous-src", "go.mod"))
	missing := filepath.Join(t.TempDir(), "no-such-repo")

	_, _ = replay(config{Repo: missing, Work: work, Keep: true, HeadBin: "candidate", PrevBin: "previous", Files: 1, Lines: 1000}, io.Discard)

	if _, err := os.Stat(filepath.Join(work, "previous-src", "go.mod")); err != nil {
		t.Errorf("-keep removed the work directory: %v", err)
	}
}
