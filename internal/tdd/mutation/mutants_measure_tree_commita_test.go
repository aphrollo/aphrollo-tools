package mutation

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func TestGitDiffOut_ReadsTheIndexAGitCommitDashABuilds(t *testing.T) {
	repo := tddtest.CommitAIndexRepo(t)
	got, stderr, err := gitDiffOut(repo, "show", ":f.txt")
	if err != nil || got != "v2\n" {
		t.Fatalf("git show :f.txt = %q (%v, stderr %q), want the staged v2", got, err, stderr)
	}
}
