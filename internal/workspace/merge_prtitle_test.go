package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A merge queue squashes a PR under its title, so a title the commit-msg gate
// would refuse (a branch name, under four words) lands on main as the subject of
// a commit that gate never saw (#1221). It is refused before the PR is enqueued.

func configuredLane(t *testing.T) *Target {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aphrollo.toml"), []byte("[aphrollo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Target{Worktree: dir, Branch: "feat/z", MainRepo: dir, RepoName: "r"}
}

func stubPRTitle(t *testing.T, title string) {
	t.Helper()
	prev := ghPRText
	ghPRText = func(string, string) (string, string, error) { return title, "", nil }
	t.Cleanup(func() { ghPRText = prev })
}

func TestMergeWait_APRTitleTheCommitGateWouldRefuseIsNotEnqueued(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	stubPRTitle(t, "lane/gatelog-out")

	var out, errb bytes.Buffer
	err := MergeWait(configuredLane(t), "squash", true, queueWait, &out, &errb)

	if err == nil {
		t.Fatal("a PR titled with its branch name was enqueued")
	}
	for _, want := range []string{"refusing to merge feat/z", "PR title", "lane/gatelog-out", "four words", "gh pr edit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if len(q.enqueued) != 0 {
		t.Errorf("enqueued %v, want nothing", q.enqueued)
	}
}

func TestMergeWait_APRTitleThatSaysWhatChangedIsEnqueued(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "QUEUED"}}
	q.polls = []qPoll{{"MERGED", nil}}
	stubPRTitle(t, "Stop reading a sibling lane's new branch as a test leak")

	var out, errb bytes.Buffer
	err := MergeWait(configuredLane(t), "squash", true, queueWait, &out, &errb)

	if err != nil {
		t.Fatalf("a well-titled PR was refused: %v\n%s", err, out.String())
	}
	if len(q.enqueued) != 1 {
		t.Errorf("enqueued %v, want the PR once", q.enqueued)
	}
}
