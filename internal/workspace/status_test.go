package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestStatus_OpenDraft(t *testing.T) {
	stubStatus(t, func(wt, branch string) (*PRStatus, error) {
		return &PRStatus{Number: 7, State: "OPEN", IsDraft: true, Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", Pass: 5}, nil
	})
	got, _ := Status(targetFor("/x", "feat/z"))
	if want := "#7 OPEN draft mergeable=MERGEABLE gate=CLEAN checks=5/5\n"; got != want {
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

// TestList_ShowsAgeDirtyAndPRState: `list` renders one line per worktree —
// path, branch ("detached" for none), whole days since the last commit, the
// dirty-file count, and the branch's PR state ("none" when there isn't one).
func TestList_ShowsAgeDirtyAndPRState(t *testing.T) {
	at := time.Now()
	stubNow(t, at)
	stubPRState(t, func(_, _ string) (string, error) { return "", nil })

	repo := initRepo(t)

	plan1, err := BuildPlan(Request{Repo: repo, Branch: "lane/x", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var out1, errb1 bytes.Buffer
	if err := Apply(plan1, &out1, &errb1); err != nil {
		t.Fatalf("Apply lane/x: %v\n%s", err, errb1.String())
	}
	wt1 := plan1.Worktree
	if err := os.WriteFile(filepath.Join(wt1, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan2, err := BuildPlan(Request{Repo: repo, Branch: "feat-old", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var out2, errb2 bytes.Buffer
	if err := Apply(plan2, &out2, &errb2); err != nil {
		t.Fatalf("Apply feat-old: %v\n%s", err, errb2.String())
	}
	wt2 := plan2.Worktree
	if err := os.WriteFile(filepath.Join(wt2, "work.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, wt2, "add", ".")
	oldDate := at.AddDate(0, 0, -5).Format(time.RFC3339)
	t.Setenv("GIT_COMMITTER_DATE", oldDate)
	t.Setenv("GIT_AUTHOR_DATE", oldDate)
	gitRun(t, wt2, "commit", "-q", "-m", "old work")
	gitRun(t, wt2, "checkout", "-q", "--detach")

	listing, err := List(repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want1 := fmt.Sprintf("%s  lane/x  0d  1 dirty  PR none", wt1)
	if !strings.Contains(listing, want1) {
		t.Errorf("listing missing line:\n%q\ngot:\n%s", want1, listing)
	}
	want2 := fmt.Sprintf("%s  detached  5d  0 dirty  PR none", wt2)
	if !strings.Contains(listing, want2) {
		t.Errorf("listing missing line:\n%q\ngot:\n%s", want2, listing)
	}
}
