package mutation

import (
	"path/filepath"
	"testing"
)

// commitOnSibling makes a commit on the sibling lane's own branch.
func commitOnSibling(t *testing.T, sibling string) {
	t.Helper()
	write(t, sibling, "work.txt", "x\n")
	gitDo(t, sibling, "add", "work.txt")
	gitDo(t, sibling, "commit", "-qm", "sibling work")
}

// A lane made or pruned adds or drops its lane/<name> branch, and a merge
// landing on main moves it by a merge the gate's own verbs made: both happen
// on a busy box through any run.
func TestSnapshotGitWorld_ALaneBranchAndAMergeLandingOnMainAreNotAChange(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, repo string)
		work func(t *testing.T, repo, feeder string)
	}{
		{"a new lane branch", nil, func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "branch", "lane/new-work")
		}},
		{"a pruned lane branch", func(t *testing.T, repo string) {
			gitDo(t, repo, "branch", "lane/old-work")
		}, func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "branch", "-D", "lane/old-work")
		}},
		{"a fast-forward merge onto main", nil, func(t *testing.T, repo, feeder string) {
			commitOnSibling(t, feeder)
			gitDo(t, repo, "merge", "-q", "--ff-only", "feeder")
		}},
		{"a merge commit onto main", func(t *testing.T, repo string) {
			gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "main work")
		}, func(t *testing.T, repo, feeder string) {
			commitOnSibling(t, feeder)
			gitDo(t, repo, "merge", "-q", "--no-ff", "-m", "merge feeder", "feeder")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, sibling, _ := canaryLanes(t)
			feeder := filepath.Join(laneDirOf(repo), "feeder")
			gitDo(t, repo, "worktree", "add", "-q", "-b", "feeder", feeder)
			if c.prep != nil {
				c.prep(t, repo)
			}
			// The run is in a lane; main is checked out in the primary, where
			// merges land, fed by another lane.
			before := snapshotGitWorld(sibling)

			c.work(t, repo, feeder)

			if changes := before.changesTo(snapshotGitWorld(sibling)); len(changes) != 0 {
				t.Errorf("ordinary work looked like a leak: %v", changes)
			}
		})
	}
}

// What a leak does to main, or to the branch names, is still named.
func TestSnapshotGitWorld_ACommitOrAMoveOfMainAndANonLaneBranchAreStillAChange(t *testing.T) {
	cases := []struct {
		name  string
		label string
		leak  func(t *testing.T, repo, sibling string)
	}{
		{"a commit on main", "the tip of main", func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "fixture")
		}},
		{"a move of main with no merge behind it", "the tip of main", func(t *testing.T, repo, sibling string) {
			commitOnSibling(t, sibling)
			gitDo(t, repo, "update-ref", "-m", "reset: moving to sibling", "refs/heads/main", "sibling")
		}},
		{"a move of main with no reflog entry", "the tip of main", func(t *testing.T, repo, sibling string) {
			commitOnSibling(t, sibling)
			gitDo(t, repo, "update-ref", "refs/heads/main", "sibling")
		}},
		{"a merge commit followed by a fixture commit", "the tip of main", func(t *testing.T, repo, sibling string) {
			commitOnSibling(t, sibling)
			gitDo(t, repo, "merge", "-q", "--ff-only", "sibling")
			gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "fixture")
		}},
		{"a branch that is no lane's", "the branches", func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "branch", "lanes/x")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, sibling, _ := canaryLanes(t)
			before := snapshotGitWorld(repo)

			c.leak(t, repo, sibling)

			requireChange(t, before.changesTo(snapshotGitWorld(repo)), c.label)
		})
	}
}

// The edges of what counts as a merge advancing main: the entries the run
// added on top of the ones it started with, all of them merges, and at least
// one.
func TestOnlyMergesAdvancedMain_TheEdges(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		want          bool
	}{
		{"one merge on top", "t1\nmerge a: Fast-forward", "t2\nmerge b: Fast-forward\nmerge a: Fast-forward", true},
		{"a pull on top", "t1\nmerge a: Fast-forward", "t2\npull: Fast-forward\nmerge a: Fast-forward", true},
		{"a merge into an empty log", "t1\n", "t2\nmerge b: Fast-forward", true},
		{"a commit on top", "t1\nmerge a: x", "t2\ncommit: fixture\nmerge a: x", false},
		{"a merge then a commit on top", "t1\nmerge a: x", "t3\ncommit: fixture\nmerge b: x\nmerge a: x", false},
		{"nothing added but a new tip", "t1\nmerge a: x", "t2\nmerge a: x", false},
		{"an entry rewritten below the top", "t1\nmerge a: x", "t2\nmerge b: x\nmerge c: x", false},
		{"a longer log that is not this one's", "t1\nmerge a: x", "t2\nmerge b: x", false},
	}
	for _, c := range cases {
		if got := onlyMergesAdvancedMain(c.before, c.after); got != c.want {
			t.Errorf("%s: onlyMergesAdvancedMain(%q, %q) = %v, want %v", c.name, c.before, c.after, got, c.want)
		}
	}
}
