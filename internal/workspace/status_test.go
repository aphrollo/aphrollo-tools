package workspace

import (
	"bytes"
	"errors"
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

// TestResolvePRStates_RunsConcurrentlyBoundedAndReportsErrorsAsUnknown: with a
// gh seam that sleeps 200ms per call, 6 worktrees must resolve in well under
// the 1.2s a fully serial run would take (bounded to 4 concurrent, so two
// batches of ~200ms), results land back in the SAME order as the input
// regardless of which goroutine finishes first, and a seam error renders "?"
// rather than "none" (which means "no PR", a different fact from "could not
// ask").
func TestResolvePRStates_RunsConcurrentlyBoundedAndReportsErrorsAsUnknown(t *testing.T) {
	entries := make([]worktreeEntry, 6)
	for i := range entries {
		entries[i] = worktreeEntry{Path: fmt.Sprintf("/wt/%d", i), Branch: fmt.Sprintf("feat/%d", i)}
	}
	stubPRState(t, func(_, branch string) (string, error) {
		// real-time: proves real bounded-concurrency overlap across goroutines
		time.Sleep(200 * time.Millisecond)
		if branch == "feat/3" {
			return "", errors.New("gh: boom")
		}
		return "OPEN", nil
	})

	start := time.Now()
	states := resolvePRStates(entries)
	elapsed := time.Since(start)

	if elapsed >= time.Second {
		t.Errorf("resolvePRStates took %s for 6 lookups at 200ms each — want overlap (bounded to 4 concurrent), not serial", elapsed)
	}
	if len(states) != len(entries) {
		t.Fatalf("len(states) = %d, want %d", len(states), len(entries))
	}
	for i, s := range states {
		want := "OPEN"
		if i == 3 {
			want = "?"
		}
		if s != want {
			t.Errorf("states[%d] = %q, want %q", i, s, want)
		}
	}
}

// TestResolvePRStates_TimesOutToUnknown: a lookup that outruns
// listPRLookupTimeout renders "?" rather than blocking the whole render —
// the var is shrunk here (like gitNetworkTimeout/ghTimeout elsewhere in this
// package) so the test proves the deadline fires without waiting out a real
// one.
func TestResolvePRStates_TimesOutToUnknown(t *testing.T) {
	oldTimeout := listPRLookupTimeout
	listPRLookupTimeout = 30 * time.Millisecond
	t.Cleanup(func() { listPRLookupTimeout = oldTimeout })
	stubPRState(t, func(_, _ string) (string, error) {
		// real-time: proves the real context deadline fires past its window
		time.Sleep(500 * time.Millisecond)
		return "OPEN", nil
	})

	start := time.Now()
	states := resolvePRStates([]worktreeEntry{{Path: "/wt/0", Branch: "feat/x"}})
	elapsed := time.Since(start)

	if elapsed >= 200*time.Millisecond {
		t.Errorf("resolvePRStates should have returned at the shrunk timeout, took %s", elapsed)
	}
	if len(states) != 1 || states[0] != "?" {
		t.Errorf("states = %v, want [\"?\"]", states)
	}
}

// A stalled `gh pr view` is a real failure, not an answer about whether a PR
// exists — ghViewPRStatusReal must propagate the (timeout) error rather than
// reading any non-nil error as "no PR" (the same shape #348 fixed in
// ghViewPRReal; this one was recorded separately as #351).
func TestGhViewPRStatusReal_ATimeoutIsPropagatedNotReadAsNoPR(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	putSlowGHStubOnPath(t)
	t.Setenv("SLOWSTUB_SLEEP_MS", "3000")
	defer func(d time.Duration) { ghTimeout = d }(ghTimeout)
	ghTimeout = 200 * time.Millisecond

	s, err := ghViewPRStatusReal(repo, "feat/x")
	if err == nil {
		t.Fatal("a timed-out gh api pulls must return an error, not be read as no-PR")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should name the timeout, got: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil status on a real failure, got %+v", s)
	}
}

// ratchet: test_removed TestGhViewPRStatusReal_StillReadsGhsOwnNoPRMessageAsAbsence: replaced by TestGhViewPRStatusReal_StillReadsAnEmptyListAsAbsence — ghViewPRStatusReal moved to REST (#880), where absence is an empty list, not a message on gh's own stderr to sniff
//
// The legitimate case must still work: REST's list-pulls endpoint answering
// an empty array is absence, not a failure, and must still yield (nil, nil).
func TestGhViewPRStatusReal_StillReadsAnEmptyListAsAbsence(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	putSlowGHStubOnPath(t)
	// No SLOWSTUB_STDOUT set: the stub prints nothing and exits 0, the same
	// shape `--jq ".[0].number // empty"` produces for a real empty pulls list.

	s, err := ghViewPRStatusReal(repo, "feat/x")
	if err != nil {
		t.Fatalf("an empty pulls list must be read as absence, not an error: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil status for a branch with no PR, got %+v", s)
	}
}
