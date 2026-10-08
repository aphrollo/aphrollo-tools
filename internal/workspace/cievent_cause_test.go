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

// A settled run names how long it took: from its first check's start to its
// last check's end, so the speed report can time the CI pipeline.
func TestMergeWait_ASettledRunRecordsHowLongItTook(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	lint := run("lint", newSHA, "completed", "success")
	lint.StartedAt, lint.CompletedAt = "2026-10-08T01:00:00Z", "2026-10-08T01:02:00Z"
	test := run("go test", newSHA, "completed", "success")
	test.StartedAt, test.CompletedAt = "2026-10-08T01:00:30Z", "2026-10-08T01:10:00Z"
	pr := &fakePR{number: 12, branch: "lane/b", steps: []ciStep{{head: newSHA, checks: []CheckRun{lint, test}}}}
	install(t, &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/b": newSHA}})

	if err := MergeWait(&Target{Worktree: "/w/b", Branch: "lane/b", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("MergeWait: %v", err)
	}

	cis := ofKind(emitted(t), "ci")
	if len(cis) != 1 || cis[0].Detail["secs"] != "600" {
		t.Fatalf("ci events = %+v, want one with secs 600 (01:00:00 to 01:10:00)", cis)
	}
}

func TestCIRunSecs_IsUnknownWhenAnyCheckLacksItsTimes(t *testing.T) {
	a := CheckRun{StartedAt: "2026-10-08T01:00:00Z", CompletedAt: "2026-10-08T01:01:00Z"}
	open := CheckRun{StartedAt: "2026-10-08T01:00:00Z"}
	if got := ciRunSecs([]CheckRun{a, open}); got != "" {
		t.Errorf("secs = %q, want none while a check has no end", got)
	}
	if got := ciRunSecs(nil); got != "" {
		t.Errorf("secs = %q, want none for no checks", got)
	}
	if got := ciRunSecs([]CheckRun{a}); got != "60" {
		t.Errorf("secs = %q, want 60", got)
	}
}
