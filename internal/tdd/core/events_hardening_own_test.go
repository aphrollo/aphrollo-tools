package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStateRoot_PrefersTheExplicitRootThenTheXdgDirectory(t *testing.T) {
	t.Setenv("TRELLIS_DATA", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join("x", "state"))
	if got, want := StateRoot(), filepath.Join("x", "state", "trellis"); got != want {
		t.Errorf("with XDG_STATE_HOME: %q, want %q", got, want)
	}
	explicit := filepath.Join(t.TempDir(), "data")
	t.Setenv("TRELLIS_DATA", explicit)
	if got := StateRoot(); got != explicit {
		t.Errorf("with TRELLIS_DATA: %q, want %q", got, explicit)
	}
}

// A relative root would move with the working directory of each hook, so one
// repo's events would land in as many places as it has directories.
func TestStateRoot_ARelativeExplicitRootIsMadeAbsolute(t *testing.T) {
	t.Setenv("TRELLIS_DATA", filepath.Join("t", "data"))

	got := StateRoot()

	want, err := filepath.Abs(filepath.Join("t", "data"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !filepath.IsAbs(got) {
		t.Fatalf("StateRoot = %q, want the absolute %q", got, want)
	}
}

// A line a crash tore has no newline, so the next record would be appended onto
// it and read as one torn line: the record must start on a line of its own.
func TestAppendEvent_ARecordAfterATornTailIsNotLost(t *testing.T) {
	isolateEvents(t)
	AppendEvent(Event{Kind: "push", Verdict: "ok"})
	f, err := os.OpenFile(eventFiles(t, "")[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"v":1,"seq":2,"kind":"to`)
	_ = f.Close()

	AppendEvent(Event{Kind: "merge", Verdict: "ok"})

	got := ReadEvents("")
	if len(got) != 2 || got[0].Kind != "push" || got[1].Kind != "merge" {
		t.Fatalf("ReadEvents = %+v, want push then merge: the torn tail swallowed the next record", got)
	}
}

// A lock file that cannot be opened must not cost the hook time or print the
// build lock's complaint: the first failure is the answer for the process, and
// the events still land, unnumbered.
func TestAppendEvent_AnUnopenableLockCostsOneFastFailureAndNoBuildLockMessage(t *testing.T) {
	isolateEvents(t)
	resetEventLockLatchForTest()
	t.Cleanup(resetEventLockLatchForTest)
	repo := eventsTestRepo(t)
	_, _, common := repoIdentity(repo)
	lockPath := eventLogFile(RepoStateDir(common), time.Now()) + ".lock"
	if err := os.MkdirAll(lockPath, 0o755); err != nil { // a directory cannot be opened as a lock file
		t.Fatal(err)
	}

	start := time.Now()
	stderr := captureStderr(t, func() {
		for range 5 {
			AppendEvent(Event{Kind: "push", Root: repo, Verdict: "ok"})
		}
	})
	took := time.Since(start)

	if took > time.Second {
		t.Errorf("5 events took %v with an unopenable lock, want well under the 2 s a single wait used to cost", took)
	}
	if strings.Contains(stderr, "build lock") {
		t.Errorf("the event lock printed the build lock's message:\n%s", stderr)
	}
	lines := eventsLines(t, repo)
	if len(lines) != 5 {
		t.Fatalf("%d events, want 5 written without the lock", len(lines))
	}
	for _, l := range lines {
		if strings.Contains(l, `"seq"`) {
			t.Errorf("line %s is numbered, want no seq written without the lock", l)
		}
	}
}

// A settled result is looked for in this month's and last month's logs only: a
// commit is not read again after the month it was recorded in has passed.
func TestAppendEventOnce_LooksBackOnlyAsFarAsLastMonth(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	now := time.Now().UTC()
	at := func(back int) string { return now.AddDate(0, -back, 0).Format(eventTimeFormat) }
	AppendEvent(Event{Kind: "ci", Root: repo, At: at(1), Detail: map[string]string{"sha": "lastmonth"}})
	AppendEvent(Event{Kind: "ci", Root: repo, At: at(3), Detail: map[string]string{"sha": "old"}})

	again := AppendEventOnce(Event{Kind: "ci", Root: repo, Detail: map[string]string{"sha": "lastmonth"}}, "sha")
	reread := AppendEventOnce(Event{Kind: "ci", Root: repo, Detail: map[string]string{"sha": "old"}}, "sha")

	if again {
		t.Error("a record of last month was not found: it is within the window")
	}
	if !reread {
		t.Error("a record three months old was found: the window is this month and the one before")
	}
}

// One failed open of the lock file is the answer for the whole process: when
// the file becomes openable later, events still skip the lock rather than try
// it again on every write.
func TestAppendEvent_AnUnopenableLockLatchesTheProcessUnnumbered(t *testing.T) {
	isolateEvents(t)
	resetEventLockLatchForTest()
	t.Cleanup(resetEventLockLatchForTest)
	repo := eventsTestRepo(t)
	_, _, common := repoIdentity(repo)
	lockPath := eventLogFile(RepoStateDir(common), time.Now()) + ".lock"
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	AppendEvent(Event{Kind: "push", Root: repo, Verdict: "ok"})
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}

	AppendEvent(Event{Kind: "merge", Verdict: "ok", Root: repo})

	lines := eventsLines(t, repo)
	if len(lines) != 2 || strings.Contains(lines[1], `"seq"`) {
		t.Fatalf("lines = %q, want two events and the second written unnumbered: the latch skips the lock", lines)
	}
}

// A record whose writer gave up on the lock is written without a number, and
// the reader numbers it from where it sits in the file: the same number the
// writer would have given it, so the sequence stays unique and grows with the
// file however many writers were waiting at once.
func TestReadEvents_NumbersARecordWrittenPastALockTimeoutFromItsPlaceInTheFile(t *testing.T) {
	isolateEvents(t)
	resetEventLockLatchForTest()
	t.Cleanup(resetEventLockLatchForTest)
	repo := eventsTestRepo(t)
	AppendEvent(Event{Kind: "push", Root: repo, Verdict: "ok"})
	_, _, common := repoIdentity(repo)
	held, err := openLockFile(eventLogFile(RepoStateDir(common), time.Now()) + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if !tryLockExclusive(held) {
		t.Fatal("could not take the event lock to hold it")
	}
	AppendEvent(Event{Kind: "merge", Root: repo, Verdict: "ok"})
	AppendEvent(Event{Kind: "ci", Root: repo, Verdict: "ok"})
	unlockFile(held)
	_ = held.Close()
	AppendEvent(Event{Kind: "escape", Root: repo, Verdict: "ok"})

	var unnumbered int
	for _, l := range eventsLines(t, repo) {
		if !strings.Contains(l, `"seq"`) {
			unnumbered++
		}
	}
	if unnumbered != 2 {
		t.Fatalf("%d records written without a number, want the 2 that waited out the held lock", unnumbered)
	}
	got := ReadEvents(repo)
	if len(got) != 4 {
		t.Fatalf("%d events read, want 4", len(got))
	}
	assertSeqGrowsWithTheFile(t, got)
}
