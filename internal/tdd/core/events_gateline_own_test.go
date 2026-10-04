package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAppendGateLog_AStageLineKeepsItsCommandAndRootOnTheEvent(t *testing.T) {
	isolateEvents(t)

	AppendGateLog("precommit", "/r", "go test ./pkg", "green", 3*time.Second)

	var e Event
	if err := json.Unmarshal([]byte(eventsLines(t, "/r")[0]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Cmd != "go test ./pkg" || e.Root != "/r" || e.Stage != "precommit" || e.Secs != 3 || e.Verdict != "green" {
		t.Errorf("event = %+v, want the line's command, root, stage, seconds and verdict", e)
	}
}

func TestReadGateEntries_ReturnsWhatTheStagesLoggedFromTheEvents(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	AppendGateLog("precommit", repo, "go test ./pkg", "green", 3*time.Second)
	AppendGateLog("precommit", repo, "go test ./pkg", "inconclusive (fail-open)", 0)
	AppendEvent(Event{Kind: "push", Root: repo, Stage: "prepush", Verdict: "ok"})
	AppendEvent(Event{Kind: "edit", Root: repo})

	got := readGateEntries(repo, time.Time{})
	if len(got) != 2 {
		t.Fatalf("entries = %+v, want the two stage lines and neither the push nor the edit", got)
	}
	if e := got[0]; e.Stage != "precommit" || e.Root != repo || e.Cmd != "go test ./pkg" || e.Verdict != "green" || e.Secs != 3 {
		t.Errorf("first entry = %+v", e)
	}
	if got[1].Verdict != "inconclusive (fail-open)" {
		t.Errorf("a verdict with spaces came back as %q", got[1].Verdict)
	}
}

func TestReadGateEntries_KeepsAWorktreesRootApartFromItsRepo(t *testing.T) {
	isolateEvents(t)
	main, lane := laneRoot(t, "lane/x")
	AppendGateLog("precommit", main, "go test ./...", "red", time.Second)
	AppendGateLog("precommit", lane, "go test ./...", "green", time.Second)
	roots := map[string]string{}
	for _, e := range readGateEntries(lane, time.Time{}) {
		roots[e.Root] = e.Verdict
	}
	if roots[main] != "red" || roots[lane] != "green" || len(roots) != 2 {
		t.Errorf("entries by root = %v, want each worktree's own verdict under its own root", roots)
	}
}

func TestReadGateEntries_SinceDropsOlderLines(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	AppendEvent(Event{Kind: "gate", Root: repo, Stage: "precommit", Verdict: "red", At: "2026-01-02T03:04:05.000Z"})
	AppendEvent(Event{Kind: "gate", Root: repo, Stage: "precommit", Verdict: "green", At: "2026-10-02T03:04:05.000Z"})
	got := readGateEntries(repo, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 || got[0].Verdict != "green" {
		t.Errorf("entries since June = %+v, want only the October one", got)
	}
}

func TestReadAllGateEntries_SpansEveryRepoOldestFirst(t *testing.T) {
	isolateEvents(t)
	a, b := eventsTestRepo(t), eventsTestRepo(t)
	AppendEvent(Event{Kind: "gate", Root: a, Stage: "precommit", Verdict: "late", At: "2026-10-02T03:04:05.000Z"})
	AppendEvent(Event{Kind: "gate", Root: b, Stage: "precommit", Verdict: "early", At: "2026-10-01T03:04:05.000Z"})
	got := readAllGateEntries(time.Time{})
	if len(got) != 2 || got[0].Verdict != "early" || got[1].Verdict != "late" {
		t.Errorf("entries = %+v, want both repos' lines, oldest first", got)
	}
}

func TestGateEntryLines_ParseBackToTheEntries(t *testing.T) {
	in := []gateEntry{
		{At: time.Date(2026, 10, 2, 3, 4, 5, 0, time.UTC), Stage: "precommit", Root: "/my projects/x", Cmd: "go test ./pkg", Verdict: "green", Secs: 1.5},
		{At: time.Date(2026, 10, 2, 3, 4, 6, 0, time.UTC), Stage: "mutants", Root: "/r", Cmd: "", Verdict: "inconclusive (fail-open)", Secs: 0},
	}
	lines := strings.Split(strings.TrimSpace(gateEntryLines(in)), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines: %q", len(lines), lines)
	}
	for i, l := range lines {
		got, ok := parseGateLine(l)
		if !ok || got.Verdict != in[i].Verdict || got.Cmd != in[i].Cmd || got.Stage != in[i].Stage || got.Root != LogToken(in[i].Root) || !got.At.Equal(in[i].At) {
			t.Errorf("line %q parsed to %+v, ok=%v; want %+v", l, got, ok, in[i])
		}
	}
}

// A window that opens inside the current month still holds this month's file:
// the file is named for its month, not for the instant a line was written.
func TestReadGateEntries_AWindowOpeningMidMonthStillReadsThatMonthsFile(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	AppendGateLog("postedit", repo, "go test ./...", "green", 0)

	got := readGateEntries(repo, time.Now().Add(-time.Minute))
	if len(got) != 1 {
		t.Fatalf("entries = %+v, want the line written a moment ago", got)
	}
}

// Where an edit's run stands is no run and no stage timing: the events say so.
func TestAppendGateLog_QueueBookkeepingIsAQueueEventAndNoNotTestedRun(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	isolateEvents(t)
	repo := eventsTestRepo(t)
	for _, verdict := range []string{"queue-waiting", "queue-started", "deferred-restart"} {
		AppendGateLog("postedit", repo, "go test ./a", verdict, 0)
	}

	for _, e := range ReadEvents(repo) {
		if e.Kind != "queue" {
			t.Errorf("verdict %q is kind %q, want queue", e.Verdict, e.Kind)
		}
		if notTestedCause(e.Verdict) != "" {
			t.Errorf("verdict %q reads as a not-tested run (%s)", e.Verdict, notTestedCause(e.Verdict))
		}
	}
}
