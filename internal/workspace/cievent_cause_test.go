package workspace

import (
	"bytes"
	"testing"
)

// A red first run is told apart by what failed: a test, the mutation check, or
// something else. The first failure class that outranks the rest names it.
func TestCICause_NamesTheClassOfTheFailedChecks(t *testing.T) {
	cases := []struct {
		failed []string
		want   string
	}{
		{[]string{"go test (ubuntu)"}, "test"},
		{[]string{"mutants-verdict"}, "mutation"},
		{[]string{"Mutation testing"}, "mutation"},
		{[]string{"lint"}, "other"},
		{nil, "other"},
		{[]string{"lint", "mutants-verdict", "go test"}, "test"},
		{[]string{"lint", "mutants-verdict"}, "mutation"},
	}
	for _, c := range cases {
		if got := ciCause(c.failed); got != c.want {
			t.Errorf("ciCause(%q) = %q, want %q", c.failed, got, c.want)
		}
	}
}

func TestMergeWait_ARedFirstRunRecordsItsCause(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cases := []struct{ check, want string }{
		{"go test", "test"},
		{"mutants-verdict", "mutation"},
		{"lint", "other"},
	}
	for _, c := range cases {
		gateState(t)
		pr := &fakePR{number: 13, branch: "lane/c", steps: []ciStep{
			{head: newSHA, checks: []CheckRun{run(c.check, newSHA, "completed", "failure")}},
		}}
		install(t, &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/c": newSHA}})

		_ = MergeWait(&Target{Worktree: "/w/c", Branch: "lane/c", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &bytes.Buffer{}, &bytes.Buffer{})

		cis := ofKind(emitted(t), "ci")
		if len(cis) != 1 || cis[0].Verdict != "red" || cis[0].Detail["cause"] != c.want {
			t.Errorf("failed check %q: ci events = %+v, want one red with cause %q", c.check, cis, c.want)
		}
	}
}

func TestMergeWait_AGreenFirstRunHasNoCause(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	pr := &fakePR{number: 11, branch: "lane/a", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "success")}},
	}}
	install(t, &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/a": newSHA}})

	if err := MergeWait(&Target{Worktree: "/w/a", Branch: "lane/a", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("MergeWait: %v", err)
	}

	cis := ofKind(emitted(t), "ci")
	if got, ok := cis[0].Detail["cause"]; len(cis) != 1 || ok {
		t.Fatalf("ci events = %+v (cause %q), want one green with no cause", cis, got)
	}
}

// The status a push or merge reads carries the cause from the failed checks'
// names, and the event the push records keeps it.
func TestPushApply_ARedCIStatusKeepsItsCauseOnTheEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	repo := pushedLane(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) {
		return CIStatus{State: "red", Failing: 1, Cause: "mutation"}, nil
	})

	p, _ := PushPlan(targetFor(repo, "feat/y"), false)
	if err := p.Apply(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	cis := ofKind(emitted(t), "ci")
	if len(cis) != 1 || cis[0].Detail["cause"] != "mutation" {
		t.Fatalf("ci events = %+v, want one red with cause mutation", cis)
	}
}

func TestGhCIStatus_ARedStatusNamesTheCauseOfItsFailedChecks(t *testing.T) {
	sha := "3333333cccccccccccccccccccccccccccccccc"
	stubChecksBySHA(t, map[string][]CheckRun{sha: {
		run("lint", sha, "completed", "success"),
		run("mutants-verdict", sha, "completed", "failure"),
	}})

	st, err := ghCIStatus("/w", sha)

	if err != nil || st.State != "red" || st.Cause != "mutation" {
		t.Fatalf("ghCIStatus = %+v, %v, want red with cause mutation", st, err)
	}
}
