package suite

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func TestGitRead_ReadsTheIndexAGitCommitDashABuilds(t *testing.T) {
	repo := tddtest.CommitAIndexRepo(t)
	got, err := gitRead(repo, "show", ":f.txt")
	if err != nil || got != "v2\n" {
		t.Fatalf("git show :f.txt = %q (%v), want the staged v2", got, err)
	}
}
