package merge

import (
	"strings"
	"testing"
)

// Under a merge queue the PR's title becomes the squash commit's subject on the
// main branch, so a title the commit-msg gate would refuse must be refused
// before the PR is enqueued, not after it is on main (#1221: "lane/gatelog-out").

func TestPRTitleIssue_AConfiguredRepoRefusesATitleTheCommitMsgGateWould(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	for _, title := range []string{"lane/gatelog-out", "Fix bug", "wip", "  lane/f31-lint-stop \n", "cmd/a.go internal/b.go"} {
		if got := PRTitleIssue(root, title); got == "" {
			t.Errorf("title %q was allowed", title)
		}
	}
	got := PRTitleIssue(root, "lane/gatelog-out")
	if !strings.Contains(got, "lane/gatelog-out") || !strings.Contains(got, "four words") {
		t.Errorf("issue = %q, want it to quote the title and say why", got)
	}
}

func TestPRTitleIssue_ATitleThatSaysWhatChangedPasses(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	for _, title := range []string{
		"Stop reading a sibling lane's new branch as a test leak",
		"  Title a multi-commit PR from its commits \n",
		"Merge branch 'lane/x' into main",
		"",
	} {
		if got := PRTitleIssue(root, title); got != "" {
			t.Errorf("title %q refused: %s", title, got)
		}
	}
}

func TestPRTitleIssue_ARepoThatNeverOptedInIsNotJudged(t *testing.T) {
	t.Parallel()
	root := gitRepo(t)
	if got := PRTitleIssue(root, "lane/gatelog-out"); got != "" {
		t.Errorf("an unconfigured repo refused a title: %s", got)
	}
}

func TestPRTitleIssue_ARepoDeclaredShapeIsExempt(t *testing.T) {
	t.Parallel()
	root := gitRepo(t)
	write(t, root, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\ncommit-message-allow = [\"^Release v\"]\n")
	if got := PRTitleIssue(root, "Release v1"); got != "" {
		t.Errorf("a title the repo allows was refused: %s", got)
	}
	if got := PRTitleIssue(root, "lane/x"); got == "" {
		t.Error("a title the repo does not allow was passed")
	}
}
