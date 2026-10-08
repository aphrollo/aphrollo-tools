package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// loggedEvents is every event of the repository the test is running in, in log
// order.
func loggedEvents(t *testing.T) []tdd.Event {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return tdd.ReadEvents(wd)
}

func eventsOfKind(evs []tdd.Event, kind string) []tdd.Event {
	var out []tdd.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// A precommit run is one commit_gate_result, whatever number of stage lines it
// also logged: counting runs must never count stages too.
func TestGatePrecommit_EmitsOneResultEventDistinctFromItsStageEvents(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	managedBlockCommit(t, false)

	if code := Run([]string{"gate", "precommit"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}

	evs := loggedEvents(t)
	results := eventsOfKind(evs, "commit_gate_result")
	if len(results) != 1 || results[0].Verdict != "blocked" {
		t.Fatalf("commit_gate_result events = %+v, want one with verdict blocked", results)
	}
	for _, e := range eventsOfKind(evs, "commit_gate") {
		if e.Stage == "result" {
			t.Fatalf("the overall result rides the stage kind: %+v", e)
		}
	}
}

func TestGatePremerge_EmitsAMergeGateResultEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	managedBlockCommit(t, true)

	if code := Run([]string{"gate", "premerge"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	results := eventsOfKind(loggedEvents(t), "merge_gate_result")
	if len(results) != 1 || results[0].Verdict != "pass" {
		t.Fatalf("merge_gate_result events = %+v, want one with verdict pass", results)
	}
}

func TestGatePrepush_EmitsAPushEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	managedBlockCommit(t, true)
	empty, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	oldStdin := os.Stdin
	os.Stdin = empty
	t.Cleanup(func() { os.Stdin = oldStdin })

	if code := Run([]string{"gate", "prepush"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	pushes := eventsOfKind(loggedEvents(t), "push")
	if len(pushes) != 1 || pushes[0].Stage != "prepush" || pushes[0].Verdict != "pass" {
		t.Fatalf("push events = %+v, want one prepush pass", pushes)
	}
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pushes[0].Detail["sha"], strings.TrimSpace(string(head)); got != want || want == "" {
		t.Fatalf("push sha = %q, want HEAD %q", got, want)
	}
}

// A filed report is one feedback event naming the tracker and never the title
// or body the person wrote.
func TestGateFeedback_EmitsAFeedbackEventWithoutTheText(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo, _ := stubIssueRepo(t, "https://github.com/o/tools/issues/5")

	code := Run([]string{"gate", "feedback", "stage rejected token SECRET99", "--repo", repo, "--upstream", "o/tools"},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	all := tdd.ReadEvents(repo)
	got := eventsOfKind(all, "feedback")
	if len(got) != 1 || got[0].Verdict != "recorded" || got[0].Detail["tracker"] != "o/tools" {
		t.Fatalf("feedback events = %+v, want one recorded for o/tools", got)
	}
	if text := fmt.Sprintf("%+v", all); strings.Contains(text, "SECRET99") {
		t.Fatalf("report text leaked into the event log: %s", text)
	}
}

// ticks makes gateClock answer each call with the next instant, stepping by step.
func ticks(t *testing.T, step time.Duration) {
	t.Helper()
	old := gateClock
	t.Cleanup(func() { gateClock = old })
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	gateClock = func() time.Time { at = at.Add(step); return at }
}

func TestGatePrecommit_StampsTheRunsElapsedSecondsOnItsResultEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	managedBlockCommit(t, false)
	ticks(t, 42*time.Second)

	Run([]string{"gate", "precommit"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	results := eventsOfKind(loggedEvents(t), "commit_gate_result")
	if len(results) != 1 || results[0].Secs != 42 {
		t.Fatalf("commit_gate_result = %+v, want one with secs 42", results)
	}
}

func TestGatePremerge_StampsTheRunsElapsedSecondsOnItsResultEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	managedBlockCommit(t, true)
	ticks(t, 17*time.Second)

	Run([]string{"gate", "premerge"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	results := eventsOfKind(loggedEvents(t), "merge_gate_result")
	if len(results) != 1 || results[0].Secs != 17 {
		t.Fatalf("merge_gate_result = %+v, want one with secs 17", results)
	}
}

// The clock is read before the first stage runs: a stage that takes 30s of the
// fake clock (the premerge routine seam stands in for it) is inside the run's
// seconds, so a start taken after the stages would read 0.
func TestGatePremerge_TheRunsSecondsIncludeTheStagesThatRunInsideIt(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	managedBlockCommit(t, true)
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	oldClock, oldSeam := gateClock, premergeRoutineSeam
	t.Cleanup(func() { gateClock, premergeRoutineSeam = oldClock, oldSeam })
	gateClock = func() time.Time { return now }
	premergeRoutineSeam = func(string) { now = now.Add(30 * time.Second) }

	Run([]string{"gate", "premerge"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	results := eventsOfKind(loggedEvents(t), "merge_gate_result")
	if len(results) != 1 || results[0].Secs != 30 {
		t.Fatalf("merge_gate_result = %+v, want one with secs 30", results)
	}
}
