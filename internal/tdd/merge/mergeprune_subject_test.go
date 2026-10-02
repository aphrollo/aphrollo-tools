package merge

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A merge this tool wrote carries a PR-style subject with no branch name in
// it (#1085). The sweep must still find the landed lane by ancestry and
// delete its branch, whatever the subject says.
func TestPruneMergedLanesAfterMerge_DeletesTheBranchOfAMergeWithAPRStyleSubject(t *testing.T) {
	mainRepo := t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)
	gitDo(t, mainRepo, "branch", "claude/session-abc")
	wt := filepath.Join(t.TempDir(), "cloud")
	gitDo(t, mainRepo, "worktree", "add", "-q", wt, "claude/session-abc")
	write(t, wt, "landed.go", "package main\n\n// landed\n")
	gitDo(t, wt, "add", "-A")
	gitDo(t, wt, "commit", "-qm", "lane work")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "Fix the debounce race (#12)", "claude/session-abc")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if len(pruned) != 1 || pruned[0].Branch != "claude/session-abc" {
		t.Fatalf("pruned = %+v, want exactly claude/session-abc (stderr %q)", pruned, errb.String())
	}
	if got := gitOutT(t, mainRepo, "branch", "--list", "claude/session-abc"); strings.TrimSpace(got) != "" {
		t.Fatalf("branch still listed after the sweep: %q", got)
	}
}
