package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// loggedEvents is every record of the test's own events.jsonl, in file order.
func loggedEvents(t *testing.T) []tdd.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tdd.StateDir(), "events.jsonl"))
	if err != nil {
		return nil
	}
	var out []tdd.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e tdd.Event
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
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
	repo, _ := stubIssueRepo(t, "https://github.com/o/tools/issues/5")

	code := Run([]string{"gate", "feedback", "stage rejected token SECRET99", "--repo", repo, "--upstream", "o/tools"},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	got := eventsOfKind(loggedEvents(t), "feedback")
	if len(got) != 1 || got[0].Verdict != "recorded" || got[0].Detail["tracker"] != "o/tools" {
		t.Fatalf("feedback events = %+v, want one recorded for o/tools", got)
	}
	data, _ := os.ReadFile(filepath.Join(tdd.StateDir(), "events.jsonl"))
	if strings.Contains(string(data), "SECRET99") {
		t.Fatalf("report text leaked into the event log: %s", data)
	}
}
