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
	got := ReadEvents(repo)
	if len(got) != 5 {
		t.Fatalf("%d events, want 5 written without the lock", len(got))
	}
	for _, e := range got {
		if e.Seq != 0 {
			t.Errorf("event %+v is numbered, want seq 0 without the lock", e)
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

	got := ReadEvents(repo)
	if len(got) != 2 || got[1].Seq != 0 {
		t.Fatalf("ReadEvents = %+v, want two events and the second unnumbered: the latch skips the lock", got)
	}
}
