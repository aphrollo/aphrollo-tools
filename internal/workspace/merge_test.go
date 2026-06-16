package workspace

import (
	"bytes"
	"strings"
	"testing"
)

// stubMerge swaps the gh merge seam (and the view seam it depends on) for a test.
func stubMerge(t *testing.T, view func(wt, branch string) (*PRInfo, error), merge func(wt, branch, method string, del bool) error) {
	t.Helper()
	ov, om := ghViewPR, ghMergePR
	ghViewPR, ghMergePR = view, merge
	t.Cleanup(func() { ghViewPR, ghMergePR = ov, om })
}

func TestMerge_MergesOpenPR(t *testing.T) {
	var gotMethod string
	var gotDelete bool
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 18, URL: "https://github.com/o/r/pull/18", State: "OPEN"}, nil
		},
		func(wt, branch, method string, del bool) error { gotMethod, gotDelete = method, del; return nil },
	)
	m, err := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if gotMethod != "squash" || !gotDelete {
		t.Errorf("merge called method=%q delete=%v, want squash/true", gotMethod, gotDelete)
	}
	if !strings.Contains(out.String(), "merged PR #18 (squash)") {
		t.Errorf("output missing merge confirmation:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "cleanup") {
		t.Errorf("output should point at cleanup next:\n%s", out.String())
	}
}

func TestMerge_NoPR(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt, branch, method string, del bool) error {
			t.Fatal("merge must not run without a PR")
			return nil
		},
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
	var gotDelete = true
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 5, URL: "u"}, nil },
		func(wt, branch, method string, del bool) error { gotDelete = del; return nil },
	)
	m, _ := MergePlan(targetFor("/x", "feat"), "merge", false /*deleteBranch*/)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatal(err)
	}
	if gotDelete {
		t.Error("--keep-branch should pass delete=false to gh")
	}
	if strings.Contains(out.String(), "deleted branch") {
		t.Errorf("keep-branch output should not claim a deletion:\n%s", out.String())
	}
}
