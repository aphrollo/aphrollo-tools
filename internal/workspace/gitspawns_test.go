package workspace

import (
	"bytes"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// What a verb costs in git children, counted by internal/run over the real
// repository the verb works in. A ceiling here is a claim about the verb: a git
// call added back to a path that reads the same fact from a file fails it.

func spawnGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// laneWithOrigin is a main checkout whose origin is a bare repository holding
// main, and a lane worktree on lane/x with one commit pushed to origin.
func laneWithOrigin(t *testing.T) (main, lane string) {
	t.Helper()
	main = initRepo(t)
	bare := filepath.Join(t.TempDir(), "origin.git")
	spawnGit(t, main, "clone", "-q", "--bare", main, bare)
	spawnGit(t, main, "remote", "add", "origin", bare)
	spawnGit(t, main, "fetch", "-q", "origin")
	spawnGit(t, main, "remote", "set-head", "origin", "main")
	lane = filepath.Join(t.TempDir(), "lane-x")
	spawnGit(t, main, "worktree", "add", "-q", "-b", "lane/x", lane)
	writeFile(t, lane, "x.txt", "x\n")
	spawnGit(t, lane, "add", ".")
	spawnGit(t, lane, "commit", "-q", "-m", "lane work")
	spawnGit(t, lane, "push", "-q", "origin", "lane/x")
	return main, lane
}

func TestGitSpawns_WorkspaceCommit(t *testing.T) {
	_, lane := laneWithOrigin(t)
	writeFile(t, lane, "new.txt", "hello\n")

	before := childrun.Started("git")
	c, err := CommitPlan(targetFor(lane, "lane/x"), "add new.txt", true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	got := childrun.Started("git") - before
	t.Logf("workspace commit: %d git spawns", got)
	if got > commitSpawnCeiling {
		t.Errorf("workspace commit started %d git children, want at most %d", got, commitSpawnCeiling)
	}
}

func TestGitSpawns_WorkspaceMerge(t *testing.T) {
	main, lane := laneWithOrigin(t)
	head := spawnGit(t, lane, "rev-parse", "HEAD")
	gateState(t)
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 5, URL: "u", State: "OPEN", HeadSHA: head}, nil
		},
		func(wt, branch, method, sha string) error { return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	laneAtHead = laneAtHeadReal
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	prevGate, prevRetro := premergeGate, postMergeRetro
	premergeGate = func(*Target, string, *tdd.CIVerdict, io.Writer) error { return nil }
	postMergeRetro = func(string, string, string, int, io.Writer) {}
	t.Cleanup(func() { premergeGate, postMergeRetro = prevGate, prevRetro })

	m, err := MergePlan(&Target{Worktree: lane, Branch: "lane/x", MainRepo: main, RepoName: "r"}, "squash", false)
	if err != nil {
		t.Fatal(err)
	}
	before := childrun.Started("git")
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s%s", err, out.String(), errb.String())
	}
	got := childrun.Started("git") - before
	t.Logf("workspace merge: %d git spawns", got)
	if got > mergeSpawnCeiling {
		t.Errorf("workspace merge started %d git children, want at most %d", got, mergeSpawnCeiling)
	}
}

// The ceilings are the measured counts: commit was 8 and merge 9 before the
// verbs read the facts that sit in files from those files.
const (
	commitSpawnCeiling = 5
	mergeSpawnCeiling  = 4
)
