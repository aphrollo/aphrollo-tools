package lawgate

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func TestGitBatchBlobs_ReadsTheIndexAGitCommitDashABuilds(t *testing.T) {
	repo := tddtest.CommitAIndexRepo(t)
	got := gitBatchBlobs(repo, "", []string{"f.txt"})
	if got["f.txt"] != "v2\n" {
		t.Fatalf("staged f.txt = %q, want v2", got["f.txt"])
	}
}
