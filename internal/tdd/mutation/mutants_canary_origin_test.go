package mutation

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A repository with an origin whose main is where the repository's main is,
// fetched: the state every lane of the real repository stands in.
func canaryRepoWithOrigin(t *testing.T) (repo, sibling string) {
	t.Helper()
	repo, sibling, _ = canaryLanes(t)
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitDo(t, filepath.Dir(bare), "clone", "-q", "--bare", repo, bare)
	gitDo(t, repo, "remote", "add", "origin", bare)
	gitDo(t, repo, "fetch", "-q", "origin")
	return repo, sibling
}

// feedOrigin makes a commit in the feeder lane and moves the fetched
// origin/main there, as a fetch after a PR merged does.
func feedOrigin(t *testing.T, repo, feeder string) {
	t.Helper()
	commitOnSibling(t, feeder)
	gitDo(t, repo, "update-ref", "refs/remotes/origin/main", "feeder")
}

// Another lane's normal merge: origin/main moved on, and main fast-forwarded
// to it. The reflog holds more than the window the canary reads, so the merge
// entry pushes the oldest one out and the log does not grow.
func TestSnapshotGitWorld_AFastForwardToOriginMainIsNotAChangeWithAFullReflog(t *testing.T) {
	repo, sibling := canaryRepoWithOrigin(t)
	for i := range reflogWindow + 5 {
		gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("setup %d", i))
		gitDo(t, repo, "update-ref", "refs/remotes/origin/main", "main")
	}
	feeder := filepath.Join(laneDirOf(repo), "feeder")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "feeder", feeder)
	before := snapshotGitWorld(sibling)

	feedOrigin(t, repo, feeder)
	gitDo(t, repo, "merge", "-q", "--ff-only", "origin/main")

	if changes := before.changesTo(snapshotGitWorld(sibling)); len(changes) != 0 {
		t.Errorf("another lane's fast-forward looked like a leak: %v", changes)
	}
}

// A merge of origin/main into a main that is ahead of it: the new main holds
// origin/main, and is not on it.
func TestSnapshotGitWorld_AMergeOfOriginMainIsNotAChange(t *testing.T) {
	repo, sibling := canaryRepoWithOrigin(t)
	feeder := filepath.Join(laneDirOf(repo), "feeder")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "feeder", feeder)
	gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "unpushed main work")
	before := snapshotGitWorld(sibling)

	feedOrigin(t, repo, feeder)
	gitDo(t, repo, "merge", "-q", "--no-ff", "-m", "merge origin/main", "origin/main")

	if changes := before.changesTo(snapshotGitWorld(sibling)); len(changes) != 0 {
		t.Errorf("a merge of origin/main looked like a leak: %v", changes)
	}
}

// What the incidents were: main moved to commits origin/main does not hold.
func TestSnapshotGitWorld_FixtureCommitsOnMainAreAChangeWithAnOrigin(t *testing.T) {
	cases := []struct {
		name string
		leak func(t *testing.T, repo string)
	}{
		{"a commit on main", func(t *testing.T, repo string) {
			gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "fixture")
		}},
		{"a root commit reached under a merge subject", func(t *testing.T, repo string) {
			tree := strings.TrimSpace(gitOutT(t, repo, "hash-object", "-t", "tree", "-w", "--stdin"))
			fixture := strings.TrimSpace(gitOutT(t, repo, "commit-tree", tree, "-m", "seed"))
			gitDo(t, repo, "update-ref", "-m", "merge origin/main: seed", "refs/heads/main", fixture)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, sibling := canaryRepoWithOrigin(t)
			before := snapshotGitWorld(sibling)

			c.leak(t, repo)

			requireChange(t, before.changesTo(snapshotGitWorld(sibling)), "the tip of main")
		})
	}
}

// A commit on top of a main that is on origin/main is still a commit: the
// reflog says so whatever the graph does.
func TestSnapshotGitWorld_ACommitOnMainIsAChangeEvenWhenTheTipIsOnOrigin(t *testing.T) {
	repo, sibling := canaryRepoWithOrigin(t)
	before := snapshotGitWorld(sibling)

	gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "fixture")
	gitDo(t, repo, "update-ref", "refs/remotes/origin/main", "main")

	requireChange(t, before.changesTo(snapshotGitWorld(sibling)), "the tip of main")
}

func TestMainOriginRelation_TheEdges(t *testing.T) {
	repo, _ := canaryRepoWithOrigin(t)
	lane := repo
	main := strings.TrimSpace(gitOutT(t, repo, "rev-parse", "main"))
	if got := mainOriginRelation(lane, main); got != originBehind {
		t.Errorf("a tip equal to origin/main: %q, want %q", got, originBehind)
	}
	gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "ahead")
	ahead := strings.TrimSpace(gitOutT(t, repo, "rev-parse", "main"))
	if got := mainOriginRelation(lane, ahead); got != originAhead {
		t.Errorf("a tip holding origin/main: %q, want %q", got, originAhead)
	}
	gitDo(t, repo, "update-ref", "refs/remotes/origin/main", ahead)
	if got := mainOriginRelation(lane, main); got != originBehind {
		t.Errorf("a tip below origin/main: %q, want %q", got, originBehind)
	}
	tree := strings.TrimSpace(gitOutT(t, repo, "hash-object", "-t", "tree", "-w", "--stdin"))
	apart := strings.TrimSpace(gitOutT(t, repo, "commit-tree", tree, "-m", "apart"))
	if got := mainOriginRelation(lane, apart); got != originApart {
		t.Errorf("a tip of another history: %q, want %q", got, originApart)
	}
	gitDo(t, repo, "update-ref", "-d", "refs/remotes/origin/main")
	if got := mainOriginRelation(lane, main); got != "" {
		t.Errorf("no origin/main: %q, want none", got)
	}
}

// The reflog the canary reads is the newest reflogWindow entries, so once it
// is full a new entry pushes the oldest out. The entries a run added are what
// sits on top of the ones it started with, wherever the window ends.
func TestOnlyMergesAdvancedMain_ASlidingWindow(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		want          bool
	}{
		{"a merge pushes the oldest out", "t1\nmerge a: x\nmerge b: x", "t2\nmerge c: x\nmerge a: x", true},
		{"a commit pushes the oldest out", "t1\nmerge a: x\nmerge b: x", "t2\ncommit: fixture\nmerge a: x", false},
		{"two entries push two out", "t1\nmerge a: x\nmerge b: x\nmerge c: x", "t3\nmerge e: x\nmerge d: x\nmerge a: x", true},
		{"a window of nothing in common", "t1\nmerge a: x\nmerge b: x", "t3\nmerge d: x\nmerge c: x\nmerge e: x", false},
	}
	for _, c := range cases {
		if got := onlyMergesAdvancedMain(c.before, c.after); got != c.want {
			t.Errorf("%s: onlyMergesAdvancedMain(%q, %q) = %v, want %v", c.name, c.before, c.after, got, c.want)
		}
	}
}

// The edges of what a move of main counts as, tip and relation to origin/main
// and reflog together.
func TestMainMoveCounts_TheEdges(t *testing.T) {
	part := func(text, origin string) gitWorldPart {
		return gitWorldPart{Label: tipOfMainLabel, Present: true, Text: text, Origin: origin}
	}
	cases := []struct {
		name      string
		was, now  gitWorldPart
		wantCount bool
	}{
		{"nothing changed", part("t1\nmerge a: x", originBehind), part("t1\nmerge a: x", originBehind), false},
		{"a fast-forward to origin", part("t1\nmerge a: x", originBehind), part("t2\nmerge b: x\nmerge a: x", originBehind), false},
		{"a merge of origin", part("t1\nmerge a: x", originAhead), part("t2\nmerge b: x\nmerge a: x", originAhead), false},
		{"a merge into another history", part("t1\nmerge a: x", originBehind), part("t2\nmerge b: x\nmerge a: x", originApart), true},
		{"a commit, tip on origin", part("t1\nmerge a: x", originBehind), part("t2\ncommit: f\nmerge a: x", originBehind), true},
		{"a move with no log entry, tip on origin", part("t1\nmerge a: x", originBehind), part("t2\nmerge a: x", originBehind), false},
		{"a move with no log entry, tip apart", part("t1\nmerge a: x", originBehind), part("t2\nmerge a: x", originApart), true},
		{"a merge entry on top, same tip", part("t1\nmerge a: x", originBehind), part("t1\nmerge b: x\nmerge a: x", originBehind), false},
		{"a rewritten log, same tip", part("t1\nmerge a: x", originBehind), part("t1\nmerge b: x", originBehind), true},
		{"no origin, a merge on top", part("t1\nmerge a: x", ""), part("t2\nmerge b: x\nmerge a: x", ""), false},
		{"no origin, a move with no log entry", part("t1\nmerge a: x", ""), part("t2\nmerge a: x", ""), true},
	}
	for _, c := range cases {
		if got := mainMoveCounts(c.was, c.now); got != c.wantCount {
			t.Errorf("%s: mainMoveCounts = %v, want %v", c.name, got, c.wantCount)
		}
	}
}
