package gitx

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// During `git commit -a` the hook's git calls for the repository read the
// index the commit is building, not the stale default one.
func TestGit_ReadsTheIndexAGitCommitDashABuilds(t *testing.T) {
	repo := tddtest.CommitAIndexRepo(t)
	got, err := git(repo, "show", ":f.txt")
	if err != nil || got != "v2\n" {
		t.Fatalf("git show :f.txt = %q (%v), want the staged v2", got, err)
	}
}
