package workspace

import (
	"strings"
	"testing"
)

func TestNeedDefaultBranch_NamesTheWayOutWhenTheRepoDoesNotSayWhichIsTrunk(t *testing.T) {
	repo := initRepo(t)
	spawnGit(t, repo, "branch", "-m", "main", "develop")
	prev := trunkOf
	trunkOf = realTrunk
	t.Cleanup(func() { trunkOf = prev })

	def, err := needDefaultBranch(repo)

	if err == nil || def != "" {
		t.Fatalf("needDefaultBranch = %q, %v; want a refusal: no origin/HEAD and no main or master", def, err)
	}
	if !strings.Contains(err.Error(), "git remote set-head origin --auto") {
		t.Errorf("error %q does not name the fix", err)
	}
}

func TestNeedDefaultBranch_ReadsTrunkFromOriginHeadWhateverItIsCalled(t *testing.T) {
	main, _ := laneWithOrigin(t)
	spawnGit(t, main, "branch", "-m", "main", "trunk")
	spawnGit(t, main, "push", "-q", "origin", "trunk")
	spawnGit(t, main, "remote", "set-head", "origin", "trunk")
	prev := trunkOf
	trunkOf = realTrunk
	t.Cleanup(func() { trunkOf = prev })

	def, err := needDefaultBranch(main)

	if err != nil || def != "trunk" {
		t.Fatalf("needDefaultBranch = %q, %v; want trunk", def, err)
	}
}

func TestWorktreeEntries_ListsTheMainCheckoutFirstAndNamesADetachedOneHEAD(t *testing.T) {
	main, lane := laneWithOrigin(t)
	spawnGit(t, lane, "checkout", "-q", "--detach")

	got, err := worktreeEntries(lane)

	if err != nil || len(got) != 2 {
		t.Fatalf("worktreeEntries = %+v, %v; want two entries", got, err)
	}
	if got[0].Branch != "main" || got[1].Branch != "HEAD" {
		t.Errorf("branches = %q, %q; want main then HEAD", got[0].Branch, got[1].Branch)
	}
	if !strings.EqualFold(got[0].Path, strings.ReplaceAll(main, "\\", "/")) && !strings.EqualFold(got[0].Path, main) {
		t.Logf("main path %q vs %q (spelling only)", got[0].Path, main)
	}
}
