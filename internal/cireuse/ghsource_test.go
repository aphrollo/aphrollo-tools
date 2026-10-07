package cireuse

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stubGH answers the gh arguments a test lists, by their joined text, and
// records every call.
type stubGH struct {
	answers map[string]string
	calls   []string
	onRun   func(args []string) // side effect of a call, e.g. writing a download
}

func (s *stubGH) run(args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	s.calls = append(s.calls, joined)
	if s.onRun != nil {
		s.onRun(args)
	}
	ans, ok := s.answers[joined]
	if !ok {
		return nil, errors.New("unexpected gh call: " + joined)
	}
	return []byte(ans), nil
}

func newStub(answers map[string]string) (*ghSource, *stubGH) {
	stub := &stubGH{answers: answers}
	return &ghSource{Repo: "o/r", Workflow: ".github/workflows/pipeline.yml", Run: stub.run, TempDir: os.TempDir}, stub
}

func TestGHSource_PullsReadsTheMergeCommitAndHead(t *testing.T) {
	t.Parallel()
	src, _ := newStub(map[string]string{
		"api repos/o/r/commits/abc/pulls?per_page=100": `[
		  {"number": 42, "state": "closed", "merged_at": "2026-10-03T10:00:00Z", "merge_commit_sha": "abc", "head": {"sha": "def", "ref": "lane/x"}},
		  {"number": 43, "state": "open", "merged_at": null, "merge_commit_sha": "zzz", "head": {"sha": "ghi"}}
		]`,
	})
	got, err := src.Pulls("abc")
	if err != nil {
		t.Fatal(err)
	}
	want := []Pull{
		{Number: 42, MergedAt: "2026-10-03T10:00:00Z", MergeCommitSHA: "abc", HeadSHA: "def"},
		{Number: 43, MergedAt: "", MergeCommitSHA: "zzz", HeadSHA: "ghi"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pulls = %+v, want %+v", got, want)
	}
}

func TestGHSource_RunsAsksForThePullRequestRunsOfTheHeadAndReadsEachField(t *testing.T) {
	t.Parallel()
	src, _ := newStub(map[string]string{
		"api repos/o/r/actions/workflows/pipeline.yml/runs?event=pull_request&head_sha=def&per_page=100": `{"total_count": 1, "workflow_runs": [
		  {"id": 900, "run_number": 7, "run_attempt": 1, "event": "pull_request", "path": ".github/workflows/pipeline.yml",
		   "status": "completed", "conclusion": "success", "head_sha": "def", "html_url": "https://github.com/o/r/actions/runs/900",
		   "head_repository": {"full_name": "o/r"}}
		]}`,
	})
	got, err := src.Runs("pull_request", "def")
	if err != nil {
		t.Fatal(err)
	}
	want := []Run{{ID: 900, Number: 7, Attempt: 1, Event: "pull_request", Path: ".github/workflows/pipeline.yml",
		Status: "completed", Conclusion: "success", HeadSHA: "def", HeadRepo: "o/r", URL: "https://github.com/o/r/actions/runs/900"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %+v, want %+v", got, want)
	}
}

func TestGHSource_RunsAsksForTheEventAndHeadItIsGiven(t *testing.T) {
	t.Parallel()
	src, stub := newStub(map[string]string{
		"api repos/o/r/actions/workflows/pipeline.yml/runs?event=merge_group&head_sha=abc&per_page=100": `{"total_count": 1, "workflow_runs": [
		  {"id": 950, "run_number": 9, "run_attempt": 1, "event": "merge_group", "path": ".github/workflows/pipeline.yml",
		   "status": "completed", "conclusion": "success", "head_sha": "abc", "html_url": "https://github.com/o/r/actions/runs/950",
		   "head_repository": {"full_name": "o/r"}}
		]}`,
	})
	got, err := src.Runs("merge_group", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 950 || got[0].Event != "merge_group" || got[0].HeadSHA != "abc" {
		t.Errorf("runs = %+v, want the one merge_group run 950 on abc", got)
	}
	if len(stub.calls) != 1 {
		t.Errorf("gh called %d times, want 1: %v", len(stub.calls), stub.calls)
	}
}

func TestGHSource_JobsReadsTheFirstAttemptWithItsSteps(t *testing.T) {
	t.Parallel()
	src, _ := newStub(map[string]string{
		"api repos/o/r/actions/runs/900/attempts/1/jobs?per_page=100": `{"jobs": [
		  {"name": "test", "conclusion": "success", "steps": [
		    {"name": "Set up job", "conclusion": "success", "number": 1},
		    {"name": "Test (race + shuffle)", "conclusion": "skipped", "number": 2}]},
		  {"name": "lint", "conclusion": null, "steps": []}
		]}`,
	})
	got, err := src.Jobs(900)
	if err != nil {
		t.Fatal(err)
	}
	want := []Job{
		{Name: "test", Conclusion: "success", Steps: []Step{{Name: "Set up job", Conclusion: "success"}, {Name: "Test (race + shuffle)", Conclusion: "skipped"}}},
		{Name: "lint", Conclusion: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("jobs = %+v, want %+v", got, want)
	}
}

func TestGHSource_TestedTreeDownloadsTheRunsArtifact(t *testing.T) {
	t.Parallel()
	src, stub := newStub(map[string]string{})
	scratch := t.TempDir()
	src.TempDir = func() string { return scratch }
	var dest string
	stub.onRun = func(args []string) {
		for i, a := range args {
			if a == "-D" {
				dest = args[i+1]
				if err := os.MkdirAll(dest, 0o755); err != nil {
					t.Error(err)
				}
				if err := os.WriteFile(filepath.Join(dest, "tree"), []byte("cafe\n"), 0o644); err != nil {
					t.Error(err)
				}
			}
		}
	}
	stub.answers["run download 900 -R o/r -n tested-tree -D "+filepath.Join(scratch, "tested-tree-900")] = ""
	got, err := src.TestedTree(900)
	if err != nil {
		t.Fatal(err)
	}
	if got != "cafe" {
		t.Errorf("tree = %q, want cafe (trailing newline trimmed)", got)
	}
	if dest != filepath.Join(scratch, "tested-tree-900") {
		t.Errorf("downloaded into %q, want a directory of its own under the scratch dir", dest)
	}
}

func TestGHSource_AFailedCallIsReportedNotSwallowed(t *testing.T) {
	t.Parallel()
	src, _ := newStub(map[string]string{})
	if _, err := src.Pulls("abc"); err == nil || !strings.Contains(err.Error(), "unexpected gh call") {
		t.Errorf("Pulls error = %v, want the gh failure", err)
	}
	if _, err := src.Runs("pull_request", "def"); err == nil {
		t.Error("Runs swallowed the gh failure")
	}
	if _, err := src.Jobs(1); err == nil {
		t.Error("Jobs swallowed the gh failure")
	}
	if _, err := src.TestedTree(1); err == nil {
		t.Error("TestedTree swallowed the gh failure")
	}
}

func TestGHSource_UnparsableJSONIsAnError(t *testing.T) {
	t.Parallel()
	src, _ := newStub(map[string]string{
		"api repos/o/r/commits/abc/pulls?per_page=100": `not json`,
	})
	if _, err := src.Pulls("abc"); err == nil {
		t.Error("Pulls accepted a body that is not JSON")
	}
}

func TestGHSource_PullReadsOnePullRequestsHead(t *testing.T) {
	t.Parallel()
	src, _ := newStub(map[string]string{
		"api repos/o/r/pulls/42": `{"number": 42, "state": "open", "merged_at": null, "merge_commit_sha": "zzz", "head": {"sha": "def", "ref": "lane/x"}}`,
	})
	got, err := src.Pull(42)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Pull{Number: 42, MergeCommitSHA: "zzz", HeadSHA: "def"}); got != want {
		t.Errorf("pull = %+v, want %+v", got, want)
	}
	if _, err := src.Pull(43); err == nil {
		t.Error("Pull swallowed the gh failure")
	}
}
