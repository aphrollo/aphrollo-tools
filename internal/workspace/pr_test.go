package workspace

import (
	"bytes"
	"strings"
	"testing"
)

// stubGH swaps the gh seam for the duration of a test and restores it after.
func stubGH(t *testing.T, view func(wt, branch string) (*PRInfo, error), create func(wt string, req PRCreate) (*PRInfo, error)) {
	t.Helper()
	ov, oc := ghViewPR, ghCreatePR
	ghViewPR, ghCreatePR = view, create
	t.Cleanup(func() { ghViewPR, ghCreatePR = ov, oc })
}

func TestPR_CreatesWhenNoneExists(t *testing.T) {
	repo := repoWithRemote(t) // main is on origin
	var created *PRCreate
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil }, // no existing PR
		func(wt string, req PRCreate) (*PRInfo, error) {
			created = &req
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN"}, nil
		},
	)
	pr, err := PRPlan(targetFor(repo, "main"), "", "My title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if created == nil || created.Base != "main" || created.Title != "My title" {
		t.Fatalf("create not invoked with expected request: %+v", created)
	}
	if !strings.Contains(out.String(), "opened PR #42") {
		t.Errorf("output missing PR confirmation:\n%s", out.String())
	}
}

func TestPR_DraftReportsState(t *testing.T) {
	repo := repoWithRemote(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: req.Draft}, nil
		},
	)
	pr, err := PRPlan(targetFor(repo, "main"), "main", "", "", true) // draft
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "draft PR #42") {
		t.Errorf("draft create should say so:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pr-state: draft") {
		t.Errorf("output must carry pr-state: draft for the relay:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pr-url: https://github.com/o/r/pull/42") {
		t.Errorf("output must carry pr-url for the relay:\n%s", out.String())
	}
}

func TestPRStateWord(t *testing.T) {
	cases := []struct {
		info PRInfo
		want string
	}{
		{PRInfo{State: "OPEN", IsDraft: true}, "draft"},
		{PRInfo{State: "OPEN", IsDraft: false}, "open"},
		{PRInfo{State: "MERGED"}, "merged"},
		{PRInfo{State: "CLOSED"}, "closed"},
	}
	for _, c := range cases {
		if got := prStateWord(&c.info); got != c.want {
			t.Errorf("prStateWord(%+v) = %q, want %q", c.info, got, c.want)
		}
	}
}

func TestPR_ReusesExisting(t *testing.T) {
	repo := repoWithRemote(t)
	createCalled := false
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 7, URL: "https://github.com/o/r/pull/7", State: "OPEN"}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { createCalled = true; return nil, nil },
	)
	pr, err := PRPlan(targetFor(repo, "main"), "main", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatal(err)
	}
	if createCalled {
		t.Error("an existing PR must be reused, not re-created")
	}
	if !strings.Contains(out.String(), "PR #7 already open") {
		t.Errorf("output should report the existing PR:\n%s", out.String())
	}
}

func TestPR_BranchNotOnOrigin(t *testing.T) {
	repo := initRepo(t) // no remote at all
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) { return &PRInfo{Number: 1}, nil },
	)
	pr, err := PRPlan(targetFor(repo, "main"), "main", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err == nil || !strings.Contains(err.Error(), "push") {
		t.Fatalf("expected an error pointing at push, got: %v", err)
	}
}

func TestPRPlan_DetachedHEAD(t *testing.T) {
	if _, err := PRPlan(&Target{Worktree: "/x", Branch: "HEAD"}, "main", "", "", false); err == nil {
		t.Fatal("expected detached-HEAD PR to be rejected")
	}
}

func TestPRPlan_BaseDefaultsToMain(t *testing.T) {
	pr, err := PRPlan(targetFor("/x", "feat"), "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Create.Base != "main" {
		t.Errorf("base = %q, want main", pr.Create.Base)
	}
}

// ensureDraftPR must reuse only an OPEN PR. A MERGED or CLOSED PR is dead — push
// must NOT relink it; it falls through and opens a fresh draft instead.
func TestEnsureDraftPR_DeadPRsAreNotReused(t *testing.T) {
	for _, state := range []string{"MERGED", "CLOSED"} {
		t.Run(state, func(t *testing.T) {
			repo := pushedRepo(t)
			created := false
			stubGH(t,
				func(wt, branch string) (*PRInfo, error) {
					return &PRInfo{Number: 5, URL: "https://github.com/o/r/pull/5", State: state}, nil
				},
				func(wt string, req PRCreate) (*PRInfo, error) {
					created = true
					if !req.Draft {
						t.Error("a fresh PR opened over a dead one must be a draft")
					}
					return &PRInfo{Number: 6, URL: "https://github.com/o/r/pull/6", State: "OPEN", IsDraft: true}, nil
				},
			)
			info, verb, err := ensureDraftPR(repo, "feat/y")
			if err != nil {
				t.Fatal(err)
			}
			if !created {
				t.Fatalf("a %s PR must not be reused — ensureDraftPR must open a fresh draft", state)
			}
			if verb != "opened" || info.Number != 6 {
				t.Errorf("expected a fresh opened PR #6, got verb=%q info=%+v", verb, info)
			}
		})
	}
}

// An OPEN PR (draft or ready) is still reused — that is the idempotent path.
func TestEnsureDraftPR_OpenIsReused(t *testing.T) {
	repo := pushedRepo(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 5, URL: "https://github.com/o/r/pull/5", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { t.Fatal("open PR must be reused, not re-created"); return nil, nil },
	)
	info, verb, err := ensureDraftPR(repo, "feat/y")
	if err != nil {
		t.Fatal(err)
	}
	if verb != "reused" || info.Number != 5 {
		t.Errorf("expected reused PR #5, got verb=%q info=%+v", verb, info)
	}
}

func TestPRNumberFromURL(t *testing.T) {
	if n := prNumberFromURL("https://github.com/o/r/pull/123"); n != 123 {
		t.Errorf("prNumberFromURL = %d, want 123", n)
	}
	if n := prNumberFromURL("not-a-url"); n != 0 {
		t.Errorf("prNumberFromURL(non-numeric) = %d, want 0", n)
	}
}
