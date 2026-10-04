package workspace

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// When hosted CI cannot start a job (an Actions billing lock, a spending
// limit), GitHub still concludes every check run as `failure`, with no step
// ever run. That is an outage, not a red: the code was never judged. These
// tests pin that `merge`, `merge --wait` and the CI read tell the two apart
// (#1064), and still refuse the merge either way.

func notStartedRun(name, sha string) CheckRun {
	r := run(name, sha, "completed", "failure")
	r.NotStarted = true
	return r
}

func TestMergeWait_JobsThatNeverStartedReportCIUnavailableNotFailed(t *testing.T) {
	pinCIMode(t, tdd.CIGithub)
	pr := &fakePR{number: 193, branch: "feat/tg", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{
			notStartedRun("backend", newSHA),
			notStartedRun("frontend", newSHA),
			notStartedRun("mutants-verdict", newSHA),
			run("lint", newSHA, "completed", "skipped"),
		}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/tg": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/tg", Branch: "feat/tg", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Fatal("checks that never ran must not merge")
	}
	if len(f.merged) != 0 {
		t.Errorf("merged %v with no check ever run", f.merged)
	}
	msg := err.Error()
	if !strings.Contains(msg, "ci unavailable: jobs not started") {
		t.Errorf("refusal does not report the outage:\n%s", msg)
	}
	if strings.Contains(msg, "failed") {
		t.Errorf("an outage is reported as a failure:\n%s", msg)
	}
	if !strings.Contains(msg, "mutants-verdict  https://github.com/o/r/actions/runs/1/mutants-verdict") {
		t.Errorf("refusal does not name each job that never started:\n%s", msg)
	}
	if !strings.Contains(out.String(), "3 of 4 checks never started") || strings.Contains(out.String(), "failed") {
		t.Errorf("wait line misreports the outage:\n%s", out.String())
	}
	if f.slept != 0 {
		t.Errorf("waited %v on jobs that will never start", f.slept)
	}
}

func TestMergeWait_ARealFailureBesideAnOutageIsStillAFailure(t *testing.T) {
	pr := &fakePR{number: 194, branch: "feat/mix", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{
			notStartedRun("backend", newSHA),
			run("lint", newSHA, "completed", "failure"),
		}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/mix": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/mix", Branch: "feat/mix", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Fatal("a failed check must stop the wait")
	}
	msg := err.Error()
	if !strings.Contains(msg, "1 check(s) failed") || !strings.Contains(msg, "lint  https") {
		t.Errorf("the real failure is not reported as one:\n%s", msg)
	}
}

func TestGhCIStatus_JobsThatNeverStartedAreUnavailableNotRed(t *testing.T) {
	sha := "021f725aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	stubChecksBySHA(t, map[string][]CheckRun{sha: {
		notStartedRun("backend", sha), notStartedRun("frontend", sha), greenRun(sha),
	}})
	got, err := ghCIStatus("/x", sha)
	if err != nil {
		t.Fatalf("ghCIStatus: %v", err)
	}
	want := CIStatus{State: "unavailable", NotStarted: 2, SHA: sha}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if w := got.Word(); w != "unavailable: 2 job(s) not started on 021f725" {
		t.Errorf("Word() = %q", w)
	}

	stubChecksBySHA(t, map[string][]CheckRun{sha: {
		notStartedRun("backend", sha), run("lint", sha, "completed", "failure"),
	}})
	if got, _ := ghCIStatus("/x", sha); got.State != "red" || got.Failing != 1 {
		t.Errorf("a real failure beside an outage = %+v, want red with 1 failing", got)
	}
}

func TestMerge_CIUnavailableRefusesByNameAndRecordsNoEscape(t *testing.T) {
	pinCIMode(t, tdd.CIGithub)
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 193, URL: "u"}, nil },
		func(wt, branch, method, sha string) error {
			t.Fatal("merge must not run while CI never ran")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run"); return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) {
		return CIStatus{State: "unavailable", NotStarted: 4, SHA: "021f725aaaa"}, nil
	})
	prev := recordMergeCIEscape
	called := false
	recordMergeCIEscape = func(o tdd.CIEscapeOptions, w io.Writer) (tdd.EscapeRecord, bool) {
		called = true
		return tdd.EscapeRecord{}, false
	}
	t.Cleanup(func() { recordMergeCIEscape = prev })

	m, _ := MergePlan(targetFor("/x", "feat/tg"), "squash", true)
	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)
	if err == nil {
		t.Fatal("expected the merge to be refused while CI never ran")
	}
	if !strings.Contains(err.Error(), "ci unavailable: 4 job(s) not started on 021f725") {
		t.Errorf("refusal does not report the outage: %v", err)
	}
	if called {
		t.Error("an outage is not a red after a local green: no escape is recorded")
	}
}
