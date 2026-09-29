package tdd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// scratchFixture is a go-build dir a killed run left in the gate's lock dir,
// long past every age bar (a day and a half, so the bar where the OS cannot
// say who holds it is passed too).
func scratchFixture(t *testing.T) (repo, scratch string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	temp := t.TempDir()
	t.Cleanup(SetLockDirForTest(temp))
	scratch = filepath.Join(temp, "go-build424242")
	if err := os.MkdirAll(filepath.Join(scratch, "b001"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(scratch, "b001", "x.test")
	if err := os.WriteFile(file, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-36 * time.Hour)
	for _, p := range []string{file, filepath.Join(scratch, "b001"), scratch} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return t.TempDir(), scratch
}

func TestGCAfterRun_ReclaimsWhatAFinishedRunLeft(t *testing.T) {
	repo, scratch := scratchFixture(t)
	if freed := GCAfterRun(repo); freed < 3 {
		t.Fatalf("freed %d bytes, want at least the 3 bytes of the dead go-build dir", freed)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("the finished run's scratch %s is still there (stat err %v)", scratch, err)
	}
}

func TestGCAfterRun_NothingToDoForNoRepo(t *testing.T) {
	_, scratch := scratchFixture(t)
	if freed := GCAfterRun(""); freed != 0 {
		t.Fatalf("freed %d for an empty repo", freed)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatalf("an empty repo must sweep nothing: %v", err)
	}
}

// The queue that just finished is this very process: its own record is not a
// reason to leave its litter behind.
func TestGCAfterRun_TheCallersOwnQueueRecordDoesNotSuspendTheSweep(t *testing.T) {
	repo, scratch := scratchFixture(t)
	if err := SaveMergeQueueRecord(liveQueueIn(repo)); err != nil {
		t.Fatal(err)
	}
	GCAfterRun(repo)
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("the sweep was suspended by the caller's own queue (stat err %v)", err)
	}
}

// A queue another live process is still running may be mid-measurement in
// this repo's areas: the whole pass stands down, identified by pid AND the
// identity of the process behind it.
func TestGCAfterRun_AForeignLiveQueueSuspendsTheSweep(t *testing.T) {
	repo, scratch := scratchFixture(t)
	ppid := os.Getppid()
	id, ok := core.ProcessIdentityFn(ppid)
	if !ok {
		t.Skip("no process identity for the parent process on this platform")
	}
	rec := queueIn(repo)
	rec.PID, rec.Identity, rec.Started = ppid, id, time.Now()
	if err := SaveMergeQueueRecord(rec); err != nil {
		t.Fatal(err)
	}
	if !rec.Live() {
		t.Skip("the parent process does not read as live here")
	}
	if freed := GCAfterRun(repo); freed != 0 {
		t.Fatalf("freed %d bytes while another live queue holds the repo", freed)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatalf("scratch was swept under a live foreign queue: %v", err)
	}
}
