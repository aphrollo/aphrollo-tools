package workspace

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// stubMerge swaps the gh merge seam, the remote-branch-delete seam, and the
// view seam they depend on for a test. Branch deletion is a remote-only step
// (git push origin --delete) so the merge never checks out the default branch —
// see the comment on ghDeleteRemoteBranch.
func stubMerge(t *testing.T, view func(wt, branch string) (*PRInfo, error), merge func(wt, branch, method string) error, del func(wt, branch string) error) {
	t.Helper()
	ov, om, od := ghViewPR, ghMergePR, ghDeleteRemoteBranch
	ghViewPR, ghMergePR, ghDeleteRemoteBranch = view, merge, del
	t.Cleanup(func() { ghViewPR, ghMergePR, ghDeleteRemoteBranch = ov, om, od })
}

// stubSync swaps the post-merge sync seam so a test can observe the catch-up of
// the canonical clone without git or the network.
func stubSync(t *testing.T, fn func(repoArg string, dry bool, stdout, stderr io.Writer) error) {
	t.Helper()
	prev := syncMainClone
	syncMainClone = fn
	t.Cleanup(func() { syncMainClone = prev })
}

func TestMerge_SyncsCanonicalCloneAfterSuccess(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 7, URL: "u"}, nil },
		func(wt, branch, method string) error { return nil },
		func(wt, branch string) error { return nil },
	)
	var syncedRepo string
	var syncedDry bool
	synced := 0
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error {
		synced++
		syncedRepo, syncedDry = repoArg, dry
		return nil
	})

	// MainRepo is the canonical clone; Worktree is the linked ticket worktree.
	tgt := &Target{Worktree: "/x/.worktrees/feat", Branch: "feat/z", MainRepo: "/x/main-clone", RepoName: "r"}
	m, _ := MergePlan(tgt, "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if synced != 1 {
		t.Fatalf("expected exactly one post-merge sync, got %d", synced)
	}
	if syncedRepo != "/x/main-clone" {
		t.Errorf("sync ran against %q, want the canonical clone /x/main-clone (not the worktree)", syncedRepo)
	}
	if syncedDry {
		t.Errorf("post-merge sync must run with dry=false to actually fast-forward")
	}
}

func TestMerge_SyncFailureDoesNotFailMerge(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 9, URL: "u"}, nil },
		func(wt, branch, method string) error { return nil },
		func(wt, branch string) error { return nil },
	)
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error {
		return fmt.Errorf("clone offline")
	})

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("a post-merge sync failure must NOT fail the merge (PR already merged): %v", err)
	}
	if !strings.Contains(out.String(), "merged PR #9") {
		t.Errorf("merge should still report success:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "sync") {
		t.Errorf("best-effort sync failure should be surfaced to stderr:\n%s", errb.String())
	}
}

func TestMerge_DryDoesNotSync(t *testing.T) {
	synced := false
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error {
		synced = true
		return nil
	})
	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	// --dry renders the plan and never calls Apply — the only sync site.
	_ = m.Render(false /*apply*/)
	if synced {
		t.Error("a --dry merge must not sync the canonical clone")
	}
}

func TestMerge_MergesOpenPR(t *testing.T) {
	var gotMethod string
	var deletedBranch string
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 18, URL: "https://github.com/o/r/pull/18", State: "OPEN"}, nil
		},
		func(wt, branch, method string) error { gotMethod = method; return nil },
		func(wt, branch string) error { deletedBranch = branch; return nil },
	)
	m, err := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if gotMethod != "squash" {
		t.Errorf("merge called method=%q, want squash", gotMethod)
	}
	if deletedBranch != "feat/z" {
		t.Errorf("remote delete called for %q, want feat/z", deletedBranch)
	}
	if !strings.Contains(out.String(), "merged PR #18 (squash)") {
		t.Errorf("output missing merge confirmation:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "prune") {
		t.Errorf("output should point at prune next:\n%s", out.String())
	}
}

func TestMerge_NoPR(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run without a PR")
			return nil
		},
		func(wt, branch string) error { t.Fatal("delete must not run without a PR"); return nil },
	)
	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err == nil || !strings.Contains(err.Error(), "pr") {
		t.Fatalf("expected an error pointing at pr, got: %v", err)
	}
}

func TestMergePlan_DetachedHEAD(t *testing.T) {
	if _, err := MergePlan(&Target{Worktree: "/x", Branch: "HEAD"}, "squash", true); err == nil {
		t.Fatal("expected detached-HEAD merge to be rejected")
	}
}

func TestMergePlan_BadMethod(t *testing.T) {
	if _, err := MergePlan(targetFor("/x", "feat"), "fast-forward", true); err == nil {
		t.Fatal("expected an unsupported merge method to be rejected")
	}
}

func TestMerge_KeepBranch(t *testing.T) {
	deleteCalled := false
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 5, URL: "u"}, nil },
		func(wt, branch, method string) error { return nil },
		func(wt, branch string) error { deleteCalled = true; return nil },
	)
	m, _ := MergePlan(targetFor("/x", "feat"), "merge", false /*deleteBranch*/)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatal(err)
	}
	if deleteCalled {
		t.Error("--keep-branch must not delete the remote branch")
	}
	if strings.Contains(out.String(), "deleted branch") {
		t.Errorf("keep-branch output should not claim a deletion:\n%s", out.String())
	}
}
