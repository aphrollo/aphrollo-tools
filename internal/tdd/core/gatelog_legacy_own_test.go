package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// legacyLine appends one line to gate.log as a binary from before the events
// carried a command and a root wrote it, with the event such a binary wrote
// beside it: a stage and a verdict, nothing else.
func legacyLine(t *testing.T, repo string, age time.Duration, stage, cmd, verdict string) {
	t.Helper()
	path := GateLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "%s %s %s %s %s 2.0s\n", at.UTC().Format(time.RFC3339), stage, repo, cmd, verdict); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	AppendEvent(Event{Kind: "stage.timing", Stage: stage, Verdict: verdict, Secs: 2, At: at.UTC().Format("2006-01-02T15:04:05.000Z07:00"), Lane: "main", Repo: repo})
}

func TestReadGateEntries_FallsBackToGateLogForHistoryTheOlderEventsCannotAnswer(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	legacyLine(t, repo, 2*time.Hour, "postedit", "go test ./...", "green")

	got := readGateEntries(repo, time.Now().Add(-3*time.Hour))
	if len(got) != 1 || got[0].Cmd != "go test ./..." || got[0].Root != repo || got[0].Verdict != "green" {
		t.Fatalf("entries = %+v, want the one line gate.log holds, with its command and root", got)
	}
}

func TestReadGateEntries_TheFallbackEndsWhereTheCompleteEventsBegin(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	legacyLine(t, repo, 2*time.Hour, "postedit", "go test ./...", "green")
	AppendGateLog("postedit", repo, "go test ./pkg", "red", time.Second)

	got := readGateEntries(repo, time.Now().Add(-3*time.Hour))
	if len(got) != 2 || got[0].Cmd != "go test ./..." || got[1].Cmd != "go test ./pkg" {
		t.Fatalf("entries = %+v, want the older gate.log line then the new event, the new run once", got)
	}
}

func TestReadGateEntries_TheFallbackKeepsToTheRepositoryAsked(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	other := eventsTestRepo(t)
	legacyLine(t, repo, 2*time.Hour, "postedit", "go test ./...", "green")
	legacyLine(t, other, 90*time.Minute, "postedit", "go test ./...", "red")

	got := readGateEntries(repo, time.Time{})
	if len(got) != 1 || got[0].Verdict != "green" {
		t.Fatalf("entries = %+v, want only the asked repository's line", got)
	}
}

func TestReadAllGateEntries_FallsBackToGateLogToo(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	other := eventsTestRepo(t)
	legacyLine(t, repo, 3*time.Hour, "precommit", "go test ./...", "green")
	legacyLine(t, other, 2*time.Hour, "precommit", "go test ./...", "red")

	got := readAllGateEntries(time.Time{})
	if len(got) != 2 || got[0].Cmd != "go test ./..." || got[1].Verdict != "red" {
		t.Fatalf("entries = %+v, want both repositories' lines, oldest first, with their commands", got)
	}
}

func TestGateHistoryStart_NamesTheOldestLineThereIs(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	if _, ok := GateHistoryStart(); ok {
		t.Fatal("an empty history has no start")
	}
	legacyLine(t, repo, 5*time.Hour, "postedit", "go test ./...", "green")
	AppendGateLog("postedit", repo, "go test ./...", "green", time.Second)

	at, ok := GateHistoryStart()
	if !ok || time.Since(at) < 4*time.Hour || time.Since(at) > 6*time.Hour {
		t.Fatalf("start = %v %v, want about five hours ago", at, ok)
	}
}

func TestEventsNewerSchema_SaysWhenARecordIsFromANewerBinary(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	AppendGateLog("postedit", repo, "go test ./...", "green", time.Second)
	if v, newer := EventsNewerSchema(); newer {
		t.Fatalf("schema %d reported newer for this binary's own records", v)
	}
	files := eventFiles(t, repo)
	f, err := os.OpenFile(files[len(files)-1], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "{\"v\":%d,\"kind\":\"x\"}\n", EventSchema+1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if v, newer := EventsNewerSchema(); !newer || v != EventSchema+1 {
		t.Fatalf("EventsNewerSchema = %d, %v, want %d, true", v, newer, EventSchema+1)
	}
}
