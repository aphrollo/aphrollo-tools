package workspace

import (
	"strings"
	"testing"
)

// A check that concluded skipped did not run: a head whose every check was
// skipped has had no CI, and is never reported as having passed (#1191).

func skippedRun(sha, name string) CheckRun {
	return CheckRun{Name: name, SHA: sha, Status: "completed", Conclusion: "skipped"}
}

func TestGhCIStatus_CountsSkippedChecksSoAnAllSkippedHeadCanBeNamed(t *testing.T) {
	sha := "3333333cccccccccccccccccccccccccccccccc"
	stubChecksBySHA(t, map[string][]CheckRun{sha: {skippedRun(sha, "a"), skippedRun(sha, "b"), greenRun(sha)}})

	mixed, err := ghCIStatus("/x", sha)
	if err != nil || mixed.Checks != 3 || mixed.Skipped != 2 || mixed.AllSkipped() {
		t.Fatalf("one passing among two skipped = %+v, %v; want 3 checks, 2 skipped, not all skipped", mixed, err)
	}

	stubChecksBySHA(t, map[string][]CheckRun{sha: {skippedRun(sha, "a"), skippedRun(sha, "b")}})
	all, err := ghCIStatus("/x", sha)
	if err != nil || !all.AllSkipped() || all.Checks != 2 {
		t.Fatalf("two skipped checks = %+v, %v; want all 2 skipped", all, err)
	}
}

func TestPollState_AllSkippedSaysNoCheckRanInsteadOfPassed(t *testing.T) {
	head := &PRHead{HeadSHA: "4444444dddddddddddddddddddddddddddddddd"}
	line, done, _, _ := pollState(head, "", []CheckRun{skippedRun(head.HeadSHA, "a"), skippedRun(head.HeadSHA, "b")})
	if !done {
		t.Fatalf("skipped checks hold nothing up: done = false, line %q", line)
	}
	if strings.Contains(line, "passed") || !strings.Contains(line, "no check ran (all 2 skipped)") {
		t.Errorf("line = %q, want %q", line, "no check ran (all 2 skipped)")
	}
}

func TestCIVerdictOf_ASkippedCheckIsNotAPassedOne(t *testing.T) {
	sha := "5555555eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	prev := ghVerdictChecks
	ghVerdictChecks = func(string, string) []CheckRun { return []CheckRun{skippedRun(sha, "a")} }
	t.Cleanup(func() { ghVerdictChecks = prev })

	v := ciVerdictOf("/x", 7, sha, CIStatus{State: "green", SHA: sha})

	if len(v.Checks) != 1 || v.Checks[0].Passed {
		t.Fatalf("verdict checks = %+v, want the skipped check recorded as not passed", v.Checks)
	}
}

func TestMerge_NamesAnAllSkippedHeadAsNoCheckRanNotAsPassed(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 18, URL: "https://github.com/o/r/pull/18", State: "OPEN", HeadSHA: "6666666ffffffffffffffffffffffffffffffff"}, nil
		},
		func(wt, branch, method, sha string) error { return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) {
		return CIStatus{State: "green", SHA: sha, Checks: 3, Skipped: 3}, nil
	})
	m, err := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if strings.Contains(out.String(), "every check") || !strings.Contains(out.String(), "no check ran on 6666666 (all 3 skipped) — the local suite is the proof") {
		t.Errorf("output:\n%s\nwant the no-check-ran line, not a pass", out.String())
	}
}
