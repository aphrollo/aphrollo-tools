package workspace

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// stubMerge swaps the gh merge seam, the remote-branch-delete seam, and the
// view seam they depend on for a test. Branch deletion is a remote-only step
// (git push origin --delete) so the merge never checks out the default branch —
// see the comment on ghDeleteRemoteBranch.
func stubMerge(t *testing.T, view func(wt, branch string) (*PRInfo, error), merge func(wt, branch, method string) error, del func(wt, branch string) (bool, error)) {
	t.Helper()
	ov, om, od := ghViewPR, ghMergePR, ghDeleteRemoteBranch
	oc := escapeClosureBeforeMerge
	ghViewPR, ghMergePR, ghDeleteRemoteBranch = view, merge, del
	// Every existing merge test drives ghMergePR through this helper without
	// itself caring about the escape-closure judgment, so it defaults to a
	// pass here — a test proving the refusal (or the recording beside it)
	// overrides escapeClosureBeforeMerge itself, after calling stubMerge.
	escapeClosureBeforeMerge = func(wt string, prNumber int, w io.Writer) error { return nil }
	t.Cleanup(func() {
		ghViewPR, ghMergePR, ghDeleteRemoteBranch = ov, om, od
		escapeClosureBeforeMerge = oc
	})
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
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
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
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
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
		func(wt, branch string) (bool, error) { deletedBranch = branch; return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
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

// TestMerge_RefusesRedCI is the #385 regression: a PR whose required checks
// are failing must never reach ghMergePR, no matter what a wrapping shell
// pipeline around a separate watch would have reported.
func TestMerge_RefusesRedCI(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 363, URL: "u"}, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run while a required check is red")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run"); return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "red", Failing: 4}, nil })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)
	if err == nil {
		t.Fatal("expected merge to be refused while CI is red")
	}
	if !strings.Contains(err.Error(), "red") {
		t.Errorf("expected the refusal to name the red state, got: %v", err)
	}
}

// TestMerge_RefusesPendingCI: a required check still running must also block —
// not just an outright failure.
func TestMerge_RefusesPendingCI(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 364, URL: "u"}, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run while a required check is pending")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run"); return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)
	if err == nil {
		t.Fatal("expected merge to be refused while CI is pending")
	}
	if !strings.Contains(err.Error(), "pending") {
		t.Errorf("expected the refusal to name the pending state, got: %v", err)
	}
}

// TestMerge_RefusesWhenCIStatusCannotBeDetermined covers the corroborating
// incident on #385: a broken read (a missing tool in a pipe, a network
// hiccup) must never be treated as "nothing pending". ghCIStatus returning an
// error is exactly that "could not determine" case, and it must refuse rather
// than fall through to the merge.
func TestMerge_RefusesWhenCIStatusCannotBeDetermined(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 365, URL: "u"}, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run when CI status could not be determined")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run"); return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) {
		return CIStatus{}, fmt.Errorf("gh pr checks: network timeout")
	})

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)
	if err == nil {
		t.Fatal("expected merge to be refused when CI status could not be read")
	}
}

func TestMerge_NoPR(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run without a PR")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run without a PR"); return false, nil },
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

// TestRemoteBranchAlreadyGone_MatchesObservedMessages pins the classifier
// against the two literal message shapes seen in the wild (#410): git's own
// "does not exist", and the "cannot lock ref ... unable to resolve
// reference" rejection GitHub's remote sends when auto-delete-head-branch
// already reaped the branch before this push runs (PRs #402, #408).
func TestRemoteBranchAlreadyGone_MatchesObservedMessages(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "does not exist",
			output: "error: unable to delete 'lane/x': remote ref does not exist",
			want:   true,
		},
		{
			name: "auto-delete-head-branch rejection",
			output: "To github.com:aphrollo/aphrollo-tools.git\n" +
				" ! [remote rejected] lane/x (cannot lock ref 'refs/heads/lane/x': " +
				"unable to resolve reference 'refs/heads/lane/x')\n" +
				"error: failed to push some refs to 'github.com:aphrollo/aphrollo-tools.git'",
			want: true,
		},
		{
			name:   "an unrelated failure",
			output: "fatal: unable to access 'https://github.com/x/y.git/': Could not resolve host",
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := remoteBranchAlreadyGone(c.output); got != c.want {
				t.Errorf("remoteBranchAlreadyGone(%q) = %v, want %v", c.output, got, c.want)
			}
		})
	}
}

// TestMerge_AlreadyDeletedRemoteBranchStillSyncs is the #410 regression: a
// delete step that reports the branch already gone must NOT abort before the
// post-merge sync (fast-forwarding the primary's main) the way an error
// return used to.
func TestMerge_AlreadyDeletedRemoteBranchStillSyncs(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 402, URL: "u"}, nil },
		func(wt, branch, method string) error { return nil },
		func(wt, branch string) (bool, error) { return true, nil }, // already gone
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	synced := 0
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error {
		synced++
		return nil
	})

	m, _ := MergePlan(targetFor("/x", "lane/x"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("an already-deleted remote branch must not fail the merge: %v\n%s", err, errb.String())
	}
	if synced != 1 {
		t.Errorf("expected the post-merge sync to still run once, got %d", synced)
	}
	if !strings.Contains(out.String(), "[skip] remote branch lane/x — already deleted") {
		t.Errorf("expected an already-deleted [skip] line, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "deleted remote branch") {
		t.Errorf("must not claim a deletion that never happened:\n%s", out.String())
	}
}

func TestMerge_KeepBranch(t *testing.T) {
	deleteCalled := false
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 5, URL: "u"}, nil },
		func(wt, branch, method string) error { return nil },
		func(wt, branch string) (bool, error) { deleteCalled = true; return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
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

// A PR opened outside this tool (`gh pr create`, the web UI) never runs
// closureChecksBeforePR, so `workspace merge` runs the same judgment itself,
// once, right before landing it.
func TestMerge_RefusesWhenEscapeClosureFails(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 700, URL: "u"}, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run when the escape-closure check refuses")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run"); return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	prevClosure := escapeClosureBeforeMerge
	escapeClosureBeforeMerge = func(wt string, prNumber int, w io.Writer) error {
		return fmt.Errorf("an escape issue this PR claims to close needs a law, gate stage, or named check changed")
	}
	t.Cleanup(func() { escapeClosureBeforeMerge = prevClosure })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)
	if err == nil {
		t.Fatal("expected the merge to be refused by the escape-closure check")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("expected the refusal to name the escape-closure check, got: %v", err)
	}
}

// A green CI run passes the escape-closure judgment through unchanged — the
// green-path tests above already prove this via stubMerge's own default, but
// this pins the call happens with the PR's real number.
func TestMerge_EscapeClosureCalledWithThePRNumber(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 701, URL: "u"}, nil },
		func(wt, branch, method string) error { return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	prevClosure := escapeClosureBeforeMerge
	var gotNumber int
	escapeClosureBeforeMerge = func(wt string, prNumber int, w io.Writer) error {
		gotNumber = prNumber
		return nil
	}
	t.Cleanup(func() { escapeClosureBeforeMerge = prevClosure })
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error { return nil })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if gotNumber != 701 {
		t.Errorf("escapeClosureBeforeMerge called with PR #%d, want #701", gotNumber)
	}
}

// A red CI on a tip the local gate already proved green is recorded as an
// escape — the merge verb's own replacement for CI's escape-record job.
func TestMerge_RecordsAnEscapeWhenCIIsRedOnATipTheGateProvedGreen(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 702, URL: "u"}, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run while CI is red")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run"); return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "red", Failing: 2}, nil })
	prev := recordMergeCIEscape
	var gotRepo, gotJob string
	recordMergeCIEscape = func(o tdd.CIEscapeOptions, w io.Writer) (tdd.EscapeRecord, bool) {
		gotRepo, gotJob = o.Repo, o.Job
		return tdd.EscapeRecord{}, false
	}
	t.Cleanup(func() { recordMergeCIEscape = prev })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err == nil {
		t.Fatal("expected the merge to be refused while CI is red")
	}
	if gotRepo != "/x" || gotJob == "" {
		t.Errorf("recordMergeCIEscape called with repo=%q job=%q, want the worktree and a named job", gotRepo, gotJob)
	}
}

// A pending or unresolvable CI state is not evidence the gate missed
// anything -- only an actual red is worth recording.
func TestMerge_PendingCIDoesNotRecordAnEscape(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 703, URL: "u"}, nil },
		func(wt, branch, method string) error {
			t.Fatal("merge must not run while CI is pending")
			return nil
		},
		func(wt, branch string) (bool, error) { t.Fatal("delete must not run"); return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })
	prev := recordMergeCIEscape
	called := false
	recordMergeCIEscape = func(o tdd.CIEscapeOptions, w io.Writer) (tdd.EscapeRecord, bool) {
		called = true
		return tdd.EscapeRecord{}, false
	}
	t.Cleanup(func() { recordMergeCIEscape = prev })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err == nil {
		t.Fatal("expected the merge to be refused while CI is pending")
	}
	if called {
		t.Error("a pending CI state must not record an escape")
	}
}
