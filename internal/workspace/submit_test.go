package workspace

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// stubReady swaps the gh pr ready seam for the duration of a test.
func stubReady(t *testing.T, ready func(wt, branch string) error) {
	t.Helper()
	o := ghReadyPR
	ghReadyPR = ready
	t.Cleanup(func() { ghReadyPR = o })
}

// stubBody swaps the gh pr edit --body seam for the duration of a test.
func stubBody(t *testing.T, edit func(wt, branch, body string) error) {
	t.Helper()
	o := ghEditPRBody
	ghEditPRBody = edit
	t.Cleanup(func() { ghEditPRBody = o })
}

// pushedRepo builds a repo whose feat branch is already on origin (so submit's
// idempotent re-push is a no-op) with one commit of work.
func pushedRepo(t *testing.T) string {
	t.Helper()
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
	run("push", "-q", "-u", "origin", "feat/y")
	return repo
}

// On GREEN CI, submit flips the draft PR to ready, sets the PR body to the
// summary, and reports the in_progress -> review handoff. The receipt uses
// "draft -> in review" wording and never the literal ready_for_review.
func TestSubmit_GreenFlipsAndSetsBody(t *testing.T) {
	repo := pushedRepo(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { t.Fatal("submit must not open a PR"); return nil, nil },
	)
	flipped := false
	stubReady(t, func(wt, branch string) error { flipped = true; return nil })
	var gotBody string
	stubBody(t, func(wt, branch, body string) error { gotBody = body; return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, err := SubmitPlan(targetFor(repo, "feat/y"), "kanban drag-and-drop summary")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !flipped {
		t.Error("green CI must flip the draft PR to ready")
	}
	if gotBody != "kanban drag-and-drop summary" {
		t.Errorf("PR body = %q, want the summary", gotBody)
	}
	o := out.String()
	if !strings.Contains(o, "submitted PR #42") || !strings.Contains(o, "draft -> in review") {
		t.Errorf("receipt missing submitted/draft->in review:\n%s", o)
	}
	if !strings.Contains(o, "ci green") {
		t.Errorf("receipt missing ci green:\n%s", o)
	}
	if !strings.Contains(o, "handoff in_progress -> review") {
		t.Errorf("receipt missing handoff line:\n%s", o)
	}
	if strings.Contains(o, "ready_for_review") {
		t.Errorf("receipt must NOT surface the literal ready_for_review:\n%s", o)
	}
}

// On RED CI, submit does NOT flip, exits non-zero, and prints the blocked
// receipt naming the failing count. Re-callable.
func TestSubmit_RedBlocksAndDoesNotFlip(t *testing.T) {
	repo := pushedRepo(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { t.Fatal("red CI must NOT flip the PR ready"); return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "red", Failing: 2}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	err := s.Apply(&out, &errb)
	if err == nil {
		t.Fatal("red CI submit must return a non-nil error so the verb exits non-zero")
	}
	o := out.String()
	if !strings.Contains(o, "blocked") || !strings.Contains(o, "CI red") {
		t.Errorf("receipt should report blocked + CI red:\n%s", o)
	}
	if !strings.Contains(o, "2 failing") {
		t.Errorf("receipt should name the failing count:\n%s", o)
	}
	if !strings.Contains(o, "NOT marked ready") {
		t.Errorf("receipt should say NOT marked ready:\n%s", o)
	}
}

// On PENDING CI, submit holds: not flipped, non-zero, held receipt. Re-callable.
func TestSubmit_PendingHoldsAndDoesNotFlip(t *testing.T) {
	repo := pushedRepo(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { t.Fatal("pending CI must NOT flip the PR ready"); return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err == nil {
		t.Fatal("pending CI submit must return a non-nil error")
	}
	o := out.String()
	if !strings.Contains(o, "held") || !strings.Contains(o, "NOT marked ready yet") {
		t.Errorf("receipt should report held + NOT marked ready yet:\n%s", o)
	}
}

// An already-ready PR on green CI is idempotent: not re-flipped, still reports
// the submitted handoff so a re-driven turn is safe.
func TestSubmit_AlreadyReadyIsIdempotent(t *testing.T) {
	repo := pushedRepo(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: false}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { t.Fatal("an already-ready PR must not be re-flipped"); return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "submitted PR #42") {
		t.Errorf("already-ready submit should still report the handoff:\n%s", out.String())
	}
}

func TestSubmitPlan_DetachedHEAD(t *testing.T) {
	if _, err := SubmitPlan(&Target{Worktree: "/x", Branch: "HEAD"}, "s"); err == nil {
		t.Fatal("expected detached-HEAD submit to be rejected")
	}
}
