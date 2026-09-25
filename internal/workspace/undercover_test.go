package workspace

import (
	"strings"
	"testing"
)

// undercoverOn declares `undercover = true` in repo's aphrollo.toml.
func undercoverOn(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, repo, "aphrollo.toml", "[aphrollo]\nundercover = true\n")
}

func requireUndercoverRefusal(t *testing.T, err error, quoted ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("want an undercover refusal, got none")
	}
	for _, want := range quoted {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
}

func requireNoUndercoverRefusal(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "undercover") {
		t.Fatalf("refused an ordinary name or text: %v", err)
	}
}

func TestBuildPlan_RefusesATellLaneName(t *testing.T) {
	repo := initRepo(t)
	undercoverOn(t, repo)
	_, err := BuildPlan(Request{Repo: repo, Branch: "lane/claude-fix", NoInstall: true, NoSafeDir: true})
	requireUndercoverRefusal(t, err, `"lane/claude-fix"`, `"claude"`, "lane/<slug>")
}

func TestBuildPlan_PassesAnOrdinaryLaneName(t *testing.T) {
	repo := initRepo(t)
	undercoverOn(t, repo)
	if _, err := BuildPlan(Request{Repo: repo, Branch: "lane/cairo", NoInstall: true, NoSafeDir: true}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlan_UndercoverIsInertWhenTheRepoNeverAsked(t *testing.T) {
	repo := initRepo(t)
	if _, err := BuildPlan(Request{Repo: repo, Branch: "claude/x", NoInstall: true, NoSafeDir: true}); err != nil {
		t.Fatal(err)
	}
}

func TestClaimPlan_RefusesATellLaneName(t *testing.T) {
	repo := initRepo(t)
	undercoverOn(t, repo)
	_, err := ClaimPlan(repo, "claude/x", "api", "", true)
	requireUndercoverRefusal(t, err, `"claude/x"`, "lane/<slug>")
}

func TestClaimPlan_PassesAnOrdinaryLaneNameOnToItsOwnChecks(t *testing.T) {
	repo := initRepo(t)
	undercoverOn(t, repo)
	_, err := ClaimPlan(repo, "lane/air-fix", "api", "", true)
	requireNoUndercoverRefusal(t, err)
	if err == nil || !strings.Contains(err.Error(), "worktree not found") {
		t.Fatalf("want claim's own missing-worktree error, got %v", err)
	}
}

func TestPRPlan_RefusesATellHeadRefTitleOrBody(t *testing.T) {
	repo := initRepo(t)
	undercoverOn(t, repo)
	_, err := PRPlan(targetFor(repo, "claude/x"), "main", "Fix the timer", "", false)
	requireUndercoverRefusal(t, err, `"claude/x"`)

	_, err = PRPlan(targetFor(repo, "lane/cairo"), "main", "Fix the timer with sonnet-4", "", false)
	requireUndercoverRefusal(t, err, "PR title", "Fix the timer with sonnet-4")

	body := "Fixes the retry timer.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n"
	_, err = PRPlan(targetFor(repo, "lane/cairo"), "main", "Fix the timer", body, false)
	requireUndercoverRefusal(t, err, "PR body", "line 3", "Generated with [Claude Code]")
}

func TestPRPlan_PassesOrdinaryText(t *testing.T) {
	repo := initRepo(t)
	undercoverOn(t, repo)
	body := "Fixes the cursor pagination in the agents list.\n\nThe AIR filter is covered.\n"
	if _, err := PRPlan(targetFor(repo, "lane/cursor-pagination"), "main", "Fix cursor pagination", body, false); err != nil {
		t.Fatal(err)
	}
}

func TestPRPlan_UndercoverIsInertWhenTheRepoNeverAsked(t *testing.T) {
	repo := initRepo(t)
	if _, err := PRPlan(targetFor(repo, "claude/x"), "main", "Written with Claude", "Generated with Claude Code", false); err != nil {
		t.Fatal(err)
	}
}

func TestShipPlan_RefusesATellPRTitle(t *testing.T) {
	repo := repoWithRemote(t)
	undercoverOn(t, repo)
	writeFile(t, repo, "f.txt", "x\n")
	_, err := ShipPlan(targetFor(repo, "main"), ShipRequest{Message: "land it", StageAll: true, Title: "Claude's fix"})
	requireUndercoverRefusal(t, err, "PR title", "Claude's fix")
}

func TestShipPlan_PassesAnOrdinaryPRTitle(t *testing.T) {
	repo := repoWithRemote(t)
	undercoverOn(t, repo)
	writeFile(t, repo, "f.txt", "x\n")
	if _, err := ShipPlan(targetFor(repo, "main"), ShipRequest{Message: "land it", StageAll: true, Title: "Land the timer fix"}); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitPlan_RefusesATellHeadRefOrSummary(t *testing.T) {
	repo := pushedRepo(t)
	undercoverOn(t, repo)
	_, err := SubmitPlan(targetFor(repo, "claude/y"), "done")
	requireUndercoverRefusal(t, err, `"claude/y"`)

	_, err = SubmitPlan(targetFor(repo, "feat/y"), "done\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	requireUndercoverRefusal(t, err, "PR body", "line 3")
}

func TestSubmitPlan_PassesAnOrdinaryHeadRefAndSummary(t *testing.T) {
	repo := pushedRepo(t)
	undercoverOn(t, repo)
	if _, err := SubmitPlan(targetFor(repo, "feat/y"), "Fixes the retry timer."); err != nil {
		t.Fatal(err)
	}
}
