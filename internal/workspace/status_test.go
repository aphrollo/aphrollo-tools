package workspace

import "testing"

// stubStatus swaps the gh status seam for a test.
func stubStatus(t *testing.T, view func(wt, branch string) (*PRStatus, error)) {
	t.Helper()
	ov := ghViewPRStatus
	ghViewPRStatus = view
	t.Cleanup(func() { ghViewPRStatus = ov })
}

func TestStatus_NoPR(t *testing.T) {
	stubStatus(t, func(wt, branch string) (*PRStatus, error) { return nil, nil })
	got, err := Status(targetFor("/x", "feat/z"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "no open PR for feat/z\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStatus_Merged(t *testing.T) {
	stubStatus(t, func(wt, branch string) (*PRStatus, error) {
		return &PRStatus{Number: 252, State: "MERGED", MergedAt: "2026-06-17T09:12:33Z"}, nil
	})
	got, err := Status(targetFor("/x", "feat/z"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "#252 MERGED merged=2026-06-17T09:12:33Z\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStatus_OpenAllPass(t *testing.T) {
	stubStatus(t, func(wt, branch string) (*PRStatus, error) {
		return &PRStatus{Number: 7, State: "OPEN", Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", Pass: 5}, nil
	})
	got, _ := Status(targetFor("/x", "feat/z"))
	if want := "#7 OPEN mergeable=MERGEABLE gate=CLEAN checks=5/5\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStatus_OpenWithFailures(t *testing.T) {
	stubStatus(t, func(wt, branch string) (*PRStatus, error) {
		return &PRStatus{Number: 9, State: "OPEN", Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED", Pass: 3, Fail: 2, Pending: 1}, nil
	})
	got, _ := Status(targetFor("/x", "feat/z"))
	if want := "#9 OPEN mergeable=MERGEABLE gate=BLOCKED checks=3/6 fail=2 pending=1\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClassifyCheck(t *testing.T) {
	cases := []struct {
		in   checkEntry
		want string
	}{
		{checkEntry{Conclusion: "SUCCESS"}, "pass"},
		{checkEntry{Conclusion: "SKIPPED"}, "pass"},
		{checkEntry{Conclusion: "FAILURE"}, "fail"},
		{checkEntry{Conclusion: "TIMED_OUT"}, "fail"},
		{checkEntry{State: "SUCCESS"}, "pass"},
		{checkEntry{State: "ERROR"}, "fail"},
		{checkEntry{State: "PENDING"}, "pending"},
		{checkEntry{Status: "IN_PROGRESS"}, "pending"},
		{checkEntry{}, "pending"},
	}
	for _, c := range cases {
		if got := classifyCheck(c.in); got != c.want {
			t.Errorf("classifyCheck(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
