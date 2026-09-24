package workspace

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

// stubRetro swaps the post-merge retro seam and records each call, so a test
// reads when the retro ran relative to the merge.
func stubRetro(t *testing.T, calls *[]string) {
	t.Helper()
	prev := postMergeRetro
	postMergeRetro = func(mainRepo, worktree, branch string, pr int, stderr io.Writer) {
		*calls = append(*calls, fmt.Sprintf("retro %s %s %s #%d", mainRepo, worktree, branch, pr))
	}
	t.Cleanup(func() { postMergeRetro = prev })
}

func TestMerge_RunsTheRetroOnceTheMergeHasLanded(t *testing.T) {
	var calls []string
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 839, URL: "u"}, nil },
		func(wt, branch, method string) error { calls = append(calls, "merge"); return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error { return nil })
	stubPremergeGate(t, func(tgt *Target, log io.Writer) error { return nil })
	stubRetro(t, &calls)

	tgt := &Target{Worktree: "/x/.worktrees/feat", Branch: "lane/feat", MainRepo: "/x/main-clone", RepoName: "r"}
	m, _ := MergePlan(tgt, "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	want := []string{"merge", "retro /x/main-clone /x/.worktrees/feat lane/feat #839"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
}

func TestMerge_RefusedMergeRunsNoRetro(t *testing.T) {
	var calls []string
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 839, URL: "u"}, nil },
		func(wt, branch, method string) error { calls = append(calls, "merge"); return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "red", Failing: 1}, nil })
	stubRetro(t, &calls)

	m, _ := MergePlan(targetFor("/x", "lane/feat"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err == nil {
		t.Fatal("a red CI merged")
	}
	if len(calls) != 0 {
		t.Errorf("a refused merge ran %q", calls)
	}
}
