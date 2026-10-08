package workspace

import (
	"bytes"
	"io"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func TestMergeApply_RecordsThePRsExpectationsWithTheReleaseItFollows(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 21, URL: "u", State: "OPEN", HeadSHA: "abc123"}, nil
		},
		func(wt, branch, method, sha string) error { return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error { return nil })
	stubPremergeGate(t, func(tgt *Target, _ string, _ *tdd.CIVerdict, log io.Writer) error { return nil })
	oText, oTag := ghPRText, newestReleaseTag
	t.Cleanup(func() { ghPRText, newestReleaseTag = oText, oTag })
	ghPRText = func(string, string) (string, string, error) {
		return "t", "version: minor\nexpect: merge-queue p50 down\nexpect: wrong-blocks rate down\n", nil
	}
	newestReleaseTag = func(string) string { return "v1.34.0" }

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	if err := m.Apply(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	merges := ofKind(emitted(t), "merge")
	if len(merges) != 1 {
		t.Fatalf("merge events = %+v, want one", merges)
	}
	if got, want := merges[0].Detail["expect"], "merge-queue p50 down;wrong-blocks rate down"; got != want {
		t.Errorf("expect detail = %q, want %q", got, want)
	}
	if got, want := merges[0].Detail["after"], "1.34.0"; got != want {
		t.Errorf("after detail = %q, want %q", got, want)
	}
}

func TestMergeDetail_APRWithNoExpectLineRecordsNone(t *testing.T) {
	oText := ghPRText
	t.Cleanup(func() { ghPRText = oText })
	ghPRText = func(string, string) (string, string, error) { return "t", "version: none\n", nil }
	m := &Merge{Target: &Target{Worktree: initRepo(t)}}
	d := m.mergeDetail(7, "squash")
	if _, has := d["expect"]; has {
		t.Errorf("detail = %v, want no expect key", d)
	}
	if _, has := d["after"]; has {
		t.Errorf("detail = %v, want no after key", d)
	}
}
