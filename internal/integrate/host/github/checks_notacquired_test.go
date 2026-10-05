package github

import (
	"errors"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// A hosted job that no runner picked up ends cancelled with no step and the
// annotation below. Unlike a billing lock, asking for the job again can work, so
// the adapter marks these apart: only a cancelled job that ran no step and
// carries that annotation is NotAcquired.
func TestMarkNotStarted_FlagsACancelledJobNoHostedRunnerAcquired(t *testing.T) {
	runs := []host.Check{
		{ID: 1, App: "github-actions", Status: "completed", Conclusion: "cancelled"},
		{ID: 2, App: "github-actions", Status: "completed", Conclusion: "cancelled"},
		{ID: 3, App: "github-actions", Status: "completed", Conclusion: "failure"},
	}
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		switch args[1] {
		case "repos/{owner}/{repo}/check-runs/1/annotations":
			return []byte(`[{"message":"The job was not acquired by Runner of type hosted even after multiple attempts"}]`), nil
		case "repos/{owner}/{repo}/check-runs/2/annotations":
			return []byte(`[{"message":"The operation was canceled."}]`), nil
		}
		return []byte("0"), nil
	}

	got := s.host(originURL).markNotStarted(runs)

	want := map[int64]bool{1: true}
	for _, r := range got {
		if !r.NotStarted {
			t.Errorf("job %d ran no step but is not NotStarted", r.ID)
		}
		if r.NotAcquired != want[r.ID] {
			t.Errorf("job %d NotAcquired = %v, want %v", r.ID, r.NotAcquired, want[r.ID])
		}
	}
}

func TestRerunFailedJobs_AsksGitHubForTheRunsFailedJobsOnly(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return nil, nil }

	if err := s.host(originURL).RerunFailedJobs(987); err != nil {
		t.Fatal(err)
	}

	if len(s.calls) != 1 || !has(s.calls[0].args, "api", "--method", "POST", "repos/{owner}/{repo}/actions/runs/987/rerun-failed-jobs") {
		t.Fatalf("calls = %v, want one POST to runs/987/rerun-failed-jobs", s.calls)
	}
}

func TestRerunFailedJobs_NamesGitHubsRefusal(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) {
		return []byte("Workflow is already running"), errors.New("exit status 1")
	}

	err := s.host(originURL).RerunFailedJobs(987)

	if err == nil || !strings.Contains(err.Error(), "Workflow is already running") {
		t.Fatalf("err = %v, want one carrying gh's own words", err)
	}
}

func TestRunID_IsTheRunAnActionsCheckBelongsTo(t *testing.T) {
	cases := map[string]int64{
		"https://github.com/o/r/actions/runs/37335178842/job/99": 37335178842,
		"https://github.com/o/r/actions/runs/5":                  5,
		"https://example.com/status":                             0,
		"":                                                       0,
	}
	for url, want := range cases {
		if got := host.RunID(host.Check{URL: url}); got != want {
			t.Errorf("RunID(%q) = %d, want %d", url, got, want)
		}
	}
}
