package ratchet

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitSHA(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// laneWithOrigin is a clone of an origin whose main holds one commit, with a
// lane branch two commits ahead of it.
func laneWithOrigin(t *testing.T) (lane, mainSHA string) {
	t.Helper()
	isolateGitConfigRatchet(t)
	origin := t.TempDir()
	gitRun(t, origin, "init", "-q", "--bare", "-b", "main")
	lane = t.TempDir()
	gitRun(t, lane, "init", "-q", "-b", "main")
	gitRun(t, lane, "config", "user.email", "t@t")
	gitRun(t, lane, "config", "user.name", "t")
	write(t, filepath.Join(lane, "a.txt"), "one\n")
	gitRun(t, lane, "add", ".")
	gitRun(t, lane, "commit", "-qm", "base")
	gitRun(t, lane, "remote", "add", "origin", origin)
	gitRun(t, lane, "push", "-q", "origin", "main")
	mainSHA = gitSHA(t, lane, "rev-parse", "HEAD")
	gitRun(t, lane, "checkout", "-qb", "lane/x")
	for _, f := range []string{"b.txt", "c.txt"} {
		write(t, filepath.Join(lane, f), "x\n")
		gitRun(t, lane, "add", ".")
		gitRun(t, lane, "commit", "-qm", f)
	}
	return lane, mainSHA
}

// A lane's default base is where it left origin's default branch, not HEAD
// and not the branch tip: that is the tree its own work is judged against.
func TestDefaultBase_IsTheMergeBaseWithOriginsDefaultBranch(t *testing.T) {
	lane, mainSHA := laneWithOrigin(t)

	sha, ref, ok := DefaultBase(lane)

	if !ok || sha != mainSHA || ref != "origin/main" {
		t.Fatalf("DefaultBase = (%q, %q, %v), want (%q, %q, true)", sha, ref, ok, mainSHA, "origin/main")
	}
}

// Origin's main moving on after the lane left it does not move the base.
func TestDefaultBase_StaysAtTheForkPointWhenOriginMovesOn(t *testing.T) {
	lane, mainSHA := laneWithOrigin(t)
	gitRun(t, lane, "checkout", "-q", "main")
	write(t, filepath.Join(lane, "d.txt"), "later\n")
	gitRun(t, lane, "add", ".")
	gitRun(t, lane, "commit", "-qm", "later")
	gitRun(t, lane, "push", "-q", "origin", "main")
	gitRun(t, lane, "checkout", "-q", "lane/x")

	sha, _, ok := DefaultBase(lane)

	if !ok || sha != mainSHA {
		t.Fatalf("DefaultBase = (%q, %v), want the fork point %q", sha, ok, mainSHA)
	}
}

// A checkout with no origin has no default base: the caller keeps its skip.
func TestDefaultBase_NoOriginHasNone(t *testing.T) {
	isolateGitConfigRatchet(t)
	root := t.TempDir()
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	write(t, filepath.Join(root, "a.txt"), "one\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	if sha, ref, ok := DefaultBase(root); ok {
		t.Fatalf("DefaultBase = (%q, %q, true), want none without an origin", sha, ref)
	}
}
