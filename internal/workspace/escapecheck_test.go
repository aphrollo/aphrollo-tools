package workspace

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// A comma-listed close ("Closes #1, #2") is caught before the PR opens at
// all — the same rule CI's pr-closes-check job used to enforce after the
// fact. Pure text, no gh needed.
func TestPR_RefusesWhenBodyClosesMoreThanOneIssueByOneKeyword(t *testing.T) {
	repo := repoWithRemote(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			t.Fatal("the PR must not be created when its body cannot honour every closing keyword")
			return nil, nil
		},
	)

	pr, err := PRPlan(targetFor(repo, "main"), "", "title", "Closes #1, #2", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err == nil {
		t.Fatal("expected the PR to be refused for a comma-listed close")
	} else if !strings.Contains(err.Error(), "single keyword") {
		t.Errorf("expected the refusal to name the closing-keyword rule, got: %v", err)
	}
}

// A clean body opens exactly as before — the format check must never refuse
// the common case.
func TestPR_ACleanBodyStillOpens(t *testing.T) {
	repo := repoWithRemote(t)
	var created *PRCreate
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			created = &req
			return &PRInfo{Number: 1, URL: "u", State: "OPEN"}, nil
		},
	)

	// No closing keyword at all: the content check must see nothing to
	// verify and never reach out to gh for a real issue lookup.
	pr, err := PRPlan(targetFor(repo, "main"), "", "title", "a small unrelated fix", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if created == nil {
		t.Fatal("the PR was never created")
	}
}

// closureChecksBeforePR refuses to open the PR when the content check (the
// branch's own diff against the merge base) says an escape it closes changes
// no check — the pre-PR half of CI's escape-closure job.
func TestPR_RefusesWhenTheContentCheckSaysTheBranchClosesNoCheck(t *testing.T) {
	repo := repoWithRemote(t)
	prev := verifyClosureLocal
	var gotTexts []string
	var gotBase, gotHead string
	verifyClosureLocal = func(r string, texts []string, base, head string, w io.Writer) (bool, error) {
		gotTexts, gotBase, gotHead = texts, base, head
		return false, nil
	}
	t.Cleanup(func() { verifyClosureLocal = prev })

	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			t.Fatal("the PR must not be created when the content check refuses")
			return nil, nil
		},
	)

	pr, err := PRPlan(targetFor(repo, "main"), "", "title", "Closes #42", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err == nil {
		t.Fatal("expected the PR to be refused by the content check")
	}
	if gotBase == "" || gotHead != "HEAD" {
		t.Errorf("verifyClosureLocal called with base=%q head=%q, want a resolved base and HEAD", gotBase, gotHead)
	}
	if len(gotTexts) == 0 || gotTexts[0] != "Closes #42" {
		t.Errorf("verifyClosureLocal texts = %v, want the PR body first", gotTexts)
	}
}

// The content check runs against origin/<base>, never a possibly-stale local
// branch — the same merge-base premutants.go's own mutantsBeforePR measures
// against.
func TestPR_ContentCheckSkipsSilentlyWithNoMergeBase(t *testing.T) {
	repo := repoWithRemote(t)
	prev := verifyClosureLocal
	called := false
	verifyClosureLocal = func(r string, texts []string, base, head string, w io.Writer) (bool, error) {
		called = true
		return true, nil
	}
	t.Cleanup(func() { verifyClosureLocal = prev })

	var created *PRCreate
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			created = &req
			return &PRInfo{Number: 1, URL: "u", State: "OPEN"}, nil
		},
	)

	// base names a branch this checkout never fetched from origin, so
	// origin/<base> resolves to nothing and no merge base can be found —
	// distinct from the actual PR base (still "main", which reuseOpenPR's own
	// remoteBranchExists check needs).
	pr, err := PRPlan(targetFor(repo, "main"), "ghost-base", "title", "Closes #42", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if called {
		t.Error("verifyClosureLocal must not be called with no merge base to diff from")
	}
	if created == nil {
		t.Fatal("the PR was never created")
	}
	if !strings.Contains(out.String(), "not checked locally") {
		t.Errorf("expected a line saying the content check was skipped:\n%s", out.String())
	}
}
