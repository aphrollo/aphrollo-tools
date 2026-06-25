package workspace

import (
	"bytes"
	"fmt"
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
	// Push runs quietly inside submit: its receipt must NOT print, so no duplicate
	// pr-url:/ci lines leak through. Submit emits exactly one consolidated receipt.
	if n := strings.Count(o, "pr-url:"); n != 1 {
		t.Errorf("expected exactly one pr-url: line (no push/submit duplication), got %d:\n%s", n, o)
	}
	if n := strings.Count(o, "ci "); n != 1 {
		t.Errorf("expected exactly one 'ci ' line, got %d:\n%s", n, o)
	}
	if strings.Contains(o, "pr #42") {
		t.Errorf("push's lowercase 'pr #N' receipt must not leak through submit:\n%s", o)
	}
}

// Submit is per-worktree: it acts on exactly the Target's worktree+branch, one
// repo at a time. There is no ticket-level / multi-repo submit — push, the PR
// read, the CI read, the ready flip and the body edit ALL run against that single
// worktree. This pins the contract so a future change can't silently widen
// submit's scope past one worktree.
func TestSubmit_ActsOnExactlyOneWorktree(t *testing.T) {
	repo := pushedRepo(t)
	tgt := targetFor(repo, "feat/y")

	type call struct{ wt, branch string }
	var seen []call
	record := func(wt, branch string) { seen = append(seen, call{wt, branch}) }

	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			record(wt, branch)
			return &PRInfo{Number: 7, URL: "https://github.com/o/r/pull/7", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { t.Fatal("submit must not open a PR"); return nil, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { record(wt, branch); return CIStatus{State: "green"}, nil })
	stubReady(t, func(wt, branch string) error { record(wt, branch); return nil })
	stubBody(t, func(wt, branch, body string) error { record(wt, branch); return nil })

	s, err := SubmitPlan(tgt, "summary")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if len(seen) == 0 {
		t.Fatal("submit made no gh calls; expected at least the view/ci/ready/body seams")
	}
	for _, c := range seen {
		if c.wt != tgt.Worktree || c.branch != tgt.Branch {
			t.Errorf("submit touched %+v, want only the target worktree %q / branch %q",
				c, tgt.Worktree, tgt.Branch)
		}
	}
}

// After a SUCCESSFUL flip, a failing body edit is best-effort: submit still
// succeeds (the PR is in review; the body is cosmetic) and prints a warning.
func TestSubmit_FlipOKBodyFailsStillSucceeds(t *testing.T) {
	repo := pushedRepo(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	flipped := false
	stubReady(t, func(wt, branch string) error { flipped = true; return nil })
	stubBody(t, func(wt, branch, body string) error { return fmt.Errorf("boom") })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("a body-edit failure after a good flip must NOT fail submit: %v", err)
	}
	if !flipped {
		t.Error("the flip must have happened before the body edit")
	}
	o := out.String() + errb.String()
	if !strings.Contains(o, "warning") {
		t.Errorf("a failed body edit should print a warning:\n%s", o)
	}
	if !strings.Contains(out.String(), "handoff in_progress -> review") {
		t.Errorf("the handoff must still be reported despite the body failure:\n%s", out.String())
	}
}

// If the FLIP itself fails, submit returns the error and NEVER edits the body.
func TestSubmit_FlipFailsReturnsErrorNoBody(t *testing.T) {
	repo := pushedRepo(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { return fmt.Errorf("flip boom") })
	stubBody(t, func(wt, branch, body string) error { t.Fatal("body must NOT be edited when the flip fails"); return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err == nil {
		t.Fatal("a failing flip must return a non-nil error")
	}
}

// submit --dry previews only: it never pushes/flips/edits — no gh mutation seam fires.
func TestSubmit_DryDoesNotMutate(t *testing.T) {
	repo := pushedRepo(t)
	stubReady(t, func(wt, branch string) error { t.Fatal("--dry must not flip"); return nil })
	stubBody(t, func(wt, branch, body string) error { t.Fatal("--dry must not edit the body"); return nil })

	s, err := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	if err != nil {
		t.Fatal(err)
	}
	out := s.Render(false)
	if !strings.Contains(out, "run again without --dry") {
		t.Errorf("dry-run should tell the caller to re-run without --dry:\n%s", out)
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

// An already-ready PR on green CI is idempotent: not re-flipped, reported with a
// [skip] receipt, still emitting the handoff so a re-driven turn is safe.
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
	o := out.String()
	if !strings.Contains(o, "already in review") || !strings.Contains(o, "[skip]") {
		t.Errorf("already-ready submit should report 'already in review [skip]':\n%s", o)
	}
	if !strings.Contains(o, "PR #42") {
		t.Errorf("already-ready receipt should name the PR:\n%s", o)
	}
	if !strings.Contains(o, "handoff in_progress -> review") {
		t.Errorf("already-ready submit should still report the handoff:\n%s", o)
	}
	if strings.Contains(o, "draft -> in review") {
		t.Errorf("an already-ready PR must NOT claim a draft->in review flip:\n%s", o)
	}
}

// Submitting twice on green CI is safe: the second call sees an already-ready PR
// and reports the [skip] receipt without re-flipping.
func TestSubmit_TwiceOnGreenSkipsSecondFlip(t *testing.T) {
	repo := pushedRepo(t)
	draft := true
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: draft}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	flips := 0
	stubReady(t, func(wt, branch string) error { flips++; draft = false; return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	for i := 0; i < 2; i++ {
		s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
		var out, errb bytes.Buffer
		if err := s.Apply(&out, &errb); err != nil {
			t.Fatalf("Apply #%d: %v\n%s", i, err, errb.String())
		}
		if i == 1 && !strings.Contains(out.String(), "already in review") {
			t.Errorf("second submit should report already in review:\n%s", out.String())
		}
	}
	if flips != 1 {
		t.Errorf("ghReadyPR should have been called exactly once across two submits, got %d", flips)
	}
}

// A fresh, never-pushed branch (ahead-count > 0) exercises the "pushed N new"
// path in the submit receipt.
func TestSubmit_UnpushedBranchReportsPushedN(t *testing.T) {
	repo := repoWithRemote(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/z")
	writeFile(t, repo, "f.txt", "x\n")
	run("add", ".")
	run("commit", "-qm", "work") // one commit, NOT yet pushed → ahead > 0

	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 7, URL: "https://github.com/o/r/pull/7", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, err := SubmitPlan(targetFor(repo, "feat/z"), "summary")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	o := out.String()
	if !strings.Contains(o, "pushed 1 new commit") {
		t.Errorf("an unpushed branch should report 'pushed 1 new commit(s)':\n%s", o)
	}
	if strings.Contains(o, "already in sync") {
		t.Errorf("an unpushed branch must NOT report already in sync:\n%s", o)
	}
}

func TestSubmitPlan_DetachedHEAD(t *testing.T) {
	if _, err := SubmitPlan(&Target{Worktree: "/x", Branch: "HEAD"}, "s"); err == nil {
		t.Fatal("expected detached-HEAD submit to be rejected")
	}
}
