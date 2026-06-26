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

// The handoff is ONE-SHOT: a CONFLICTING branch STILL flips ready (the coder
// signals "done" once), exits zero, and the receipt names the conflict + the
// rebase fix. A conflicted branch can't compute a merge ref, so its CI never
// greens and the server AllOpenGreen gate never arms the reviewer until the coder
// rebases + pushes — so flipping early is safe and avoids the re-submit strand.
func TestSubmit_ConflictingFlipsAndWarnsRebase(t *testing.T) {
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
			flipped := false
			stubReady(t, func(wt, branch string) error { flipped = true; return nil })
			stubBody(t, func(wt, branch, body string) error { return nil })
			stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

			s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
			var out, errb bytes.Buffer
			if err := s.Apply(&out, &errb); err != nil {
				t.Fatalf("a conflicted branch must still hand off (exit zero): %v\n%s", err, errb.String())
			}
			if !flipped {
				t.Error("the handoff flip must happen once, even on a conflicted branch")
			}
			o := out.String()
			if !strings.Contains(o, "submitted PR #42") || !strings.Contains(o, "draft -> in review") {
				t.Errorf("receipt should report the handoff flip:\n%s", o)
			}
			if !strings.Contains(o, "merge conflict") || !strings.Contains(o, "rebase") {
				t.Errorf("receipt should name the conflict + rebase fix:\n%s", o)
			}
			// A conflict overrides the CI line: report the conflict, not "ci pending".
			if strings.Contains(o, "ci pending") {
				t.Errorf("a conflicted branch must report the conflict, not ci pending:\n%s", o)
			}
		})
	}
}

// UNKNOWN-then-CONFLICTING resolves via the bounded poll and still hands off
// (flip once) with the conflict warning.
func TestSubmit_UnknownThenConflictingFlipsAndWarns(t *testing.T) {
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
	flipped := false
	stubReady(t, func(wt, branch string) error { flipped = true; return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("UNKNOWN-then-CONFLICTING must still hand off: %v\n%s", err, errb.String())
	}
	if !flipped {
		t.Error("the handoff flip must happen once after the poll resolved conflicting")
	}
	if !strings.Contains(out.String(), "merge conflict") {
		t.Errorf("receipt should report the conflict after the poll resolved:\n%s", out.String())
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
