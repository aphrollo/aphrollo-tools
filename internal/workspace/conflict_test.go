package workspace

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// fastMergeablePoll shrinks the bounded re-poll to zero delay for tests so the
// UNKNOWN->resolved path exercises the loop without real sleeps.
func fastMergeablePoll(t *testing.T) {
	t.Helper()
	oa, od := mergeablePollAttempts, mergeablePollDelay
	mergeablePollAttempts, mergeablePollDelay = 4, 0
	t.Cleanup(func() { mergeablePollAttempts, mergeablePollDelay = oa, od })
}

func TestIsConflicting(t *testing.T) {
	cases := []struct {
		info PRInfo
		want bool
	}{
		{PRInfo{Mergeable: "CONFLICTING"}, true},
		{PRInfo{MergeStateStatus: "DIRTY"}, true},
		{PRInfo{Mergeable: "conflicting"}, true}, // case-insensitive
		{PRInfo{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"}, false},
		{PRInfo{Mergeable: "UNKNOWN"}, false}, // unknown is not (yet) a conflict
		{PRInfo{}, false},                     // unpopulated
	}
	for _, c := range cases {
		if got := isConflicting(&c.info); got != c.want {
			t.Errorf("isConflicting(%+v) = %v, want %v", c.info, got, c.want)
		}
	}
}

func TestMergeUnknown(t *testing.T) {
	cases := []struct {
		info PRInfo
		want bool
	}{
		{PRInfo{Mergeable: "UNKNOWN"}, true},
		{PRInfo{Mergeable: "unknown"}, true},
		{PRInfo{Mergeable: "MERGEABLE"}, false},
		{PRInfo{Mergeable: "CONFLICTING"}, false},
		{PRInfo{}, false}, // empty (old stub / un-requested field) is NOT treated as unknown
	}
	for _, c := range cases {
		if got := mergeUnknown(&c.info); got != c.want {
			t.Errorf("mergeUnknown(%+v) = %v, want %v", c.info, got, c.want)
		}
	}
}

// viewPRMergeable re-polls only while GitHub reports UNKNOWN, stopping the moment
// it resolves to MERGEABLE/CONFLICTING.
func TestViewPRMergeable_PollsUntilResolved(t *testing.T) {
	fastMergeablePoll(t)
	calls := 0
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			calls++
			if calls < 3 {
				return &PRInfo{Number: 1, Mergeable: "UNKNOWN"}, nil
			}
			return &PRInfo{Number: 1, Mergeable: "CONFLICTING"}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	info, err := viewPRMergeable("/wt", "feat/y")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("expected 3 polls until resolved, got %d", calls)
	}
	if !isConflicting(info) {
		t.Errorf("resolved info should be conflicting: %+v", info)
	}
}

// When GitHub never resolves within the bound, the last (UNKNOWN) info is
// returned — the caller reports unknown, never a false all-clear.
func TestViewPRMergeable_StaysUnknown(t *testing.T) {
	fastMergeablePoll(t)
	calls := 0
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			calls++
			return &PRInfo{Number: 1, Mergeable: "UNKNOWN"}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	info, err := viewPRMergeable("/wt", "feat/y")
	if err != nil {
		t.Fatal(err)
	}
	if calls != mergeablePollAttempts {
		t.Errorf("expected the full %d attempts, got %d", mergeablePollAttempts, calls)
	}
	if !mergeUnknown(info) {
		t.Errorf("stays-unknown info should remain UNKNOWN: %+v", info)
	}
}

// A CONFLICTING branch hard-blocks submit: no flip, non-zero exit, a receipt
// naming the conflicts and the rebase/resolve fix.
func TestSubmit_ConflictingBlocksAndDoesNotFlip(t *testing.T) {
	for _, field := range []string{"mergeable", "mergeState"} {
		t.Run(field, func(t *testing.T) {
			repo := pushedRepo(t)
			info := &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}
			if field == "mergeable" {
				info.Mergeable = "CONFLICTING"
			} else {
				info.MergeStateStatus = "DIRTY"
			}
			stubGH(t,
				func(wt, branch string) (*PRInfo, error) { return info, nil },
				func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
			)
			stubReady(t, func(wt, branch string) error { t.Fatal("a conflicted branch must NOT be flipped ready"); return nil })
			stubBody(t, func(wt, branch, body string) error { return nil })
			// CI is pending on a conflicted branch (checks never start) — submit must
			// still report conflicts, not "CI pending".
			stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

			s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
			var out, errb bytes.Buffer
			if err := s.Apply(&out, &errb); err == nil {
				t.Fatal("a conflicted branch must return a non-nil error so submit exits non-zero")
			}
			o := out.String()
			if !strings.Contains(o, "blocked") || !strings.Contains(o, "merge conflicts") {
				t.Errorf("receipt should report blocked + merge conflicts:\n%s", o)
			}
			if !strings.Contains(o, "NOT marked ready") {
				t.Errorf("receipt should say NOT marked ready:\n%s", o)
			}
			if !strings.Contains(o, "rebase") || !strings.Contains(o, "re-run submit") {
				t.Errorf("receipt should name the rebase/resolve fix:\n%s", o)
			}
			if strings.Contains(o, "CI pending") || strings.Contains(o, "held") {
				t.Errorf("a conflicted branch must report conflicts, not CI pending/held:\n%s", o)
			}
		})
	}
}

// UNKNOWN-then-CONFLICTING resolves via the bounded poll and still blocks.
func TestSubmit_UnknownThenConflictingBlocks(t *testing.T) {
	fastMergeablePoll(t)
	repo := pushedRepo(t)
	calls := 0
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			calls++
			m := "UNKNOWN"
			if calls >= 2 {
				m = "CONFLICTING"
			}
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true, Mergeable: m}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { t.Fatal("a conflicted branch must NOT be flipped ready"); return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err == nil {
		t.Fatal("UNKNOWN-then-CONFLICTING must block")
	}
	if !strings.Contains(out.String(), "merge conflicts") {
		t.Errorf("receipt should report merge conflicts after the poll resolved:\n%s", out.String())
	}
}

// UNKNOWN-then-MERGEABLE resolves clean via the poll: a green branch still flips.
func TestSubmit_UnknownThenMergeableGreenFlips(t *testing.T) {
	fastMergeablePoll(t)
	repo := pushedRepo(t)
	calls := 0
	draft := true
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			calls++
			m := "UNKNOWN"
			if calls >= 2 {
				m = "MERGEABLE"
			}
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: draft, Mergeable: m}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	flipped := false
	stubReady(t, func(wt, branch string) error { flipped = true; draft = false; return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("a mergeable green branch must submit: %v\n%s", err, errb.String())
	}
	if !flipped {
		t.Error("UNKNOWN-then-MERGEABLE green branch must flip to ready")
	}
	o := out.String()
	if !strings.Contains(o, "submitted PR #42") {
		t.Errorf("receipt should report the handoff:\n%s", o)
	}
	if strings.Contains(o, "merge conflicts") || strings.Contains(o, "mergeable: unknown") {
		t.Errorf("a resolved-mergeable branch must NOT report conflicts/unknown:\n%s", o)
	}
}

// push surfaces a loud conflict line on the receipt but still pushes (non-fatal).
func TestPush_ConflictingWarnsButPushes(t *testing.T) {
	repo := repoWithRemote(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/y")
	writeFile(t, repo, "f.txt", "x\n")
	run("add", ".")
	run("commit", "-qm", "work")

	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 9, URL: "https://github.com/o/r/pull/9", State: "OPEN", IsDraft: true, Mergeable: "CONFLICTING"}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { t.Fatal("existing PR must be reused"); return nil, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

	p, _ := PushPlan(targetFor(repo, "feat/y"), false)
	var out, errb bytes.Buffer
	if err := p.Apply(&out, &errb); err != nil {
		t.Fatalf("push must succeed despite conflicts (non-fatal): %v\n%s", err, errb.String())
	}
	o := out.String()
	if !strings.Contains(o, "pushed feat/y -> origin") {
		t.Errorf("push must still push:\n%s", o)
	}
	if !strings.Contains(strings.ToUpper(o), "CONFLICT") {
		t.Errorf("push receipt must carry a loud conflict warning:\n%s", o)
	}
}
