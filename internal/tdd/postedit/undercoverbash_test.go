package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #879 item 3: in a repo that keeps its history undercover, the
// Bash/PowerShell hook refuses a command that would create or push a ref
// named with a tell, or hand tell text to gh for a PR or issue, before it
// runs. The git shim, pre-push and the workspace verbs are the walls behind
// it; this one stops the command before any of them is reached.

// undercoverBashRepo is a git repo whose aphrollo.toml sets undercover as given.
func undercoverBashRepo(t *testing.T, on bool) string {
	t.Helper()
	body := ""
	if on {
		body = "undercover = true\n"
	}
	return prRepo(t, body)
}

const bashTellFooter = "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

func TestUndercoverBashDecision_RefusesATellRefAtEveryCreatingVerb(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	for _, cmd := range []string{
		"git switch -c claude/x",
		"git switch --create=lane/claude-fix",
		"git -C . branch Claude_x main",
		"git branch -m lane/old lane/opus-4-fix",
		"git worktree add -b anthropic/x ../x main",
		"git push origin HEAD:claude/x",
		"git push -u origin lane/a lane/claude-fix",
		`bash -c "git switch -c claude/x"`,
		"echo $(git branch claude/x)",
		"gh pr create --head claude/x --title t --body b",
		"gh pr create -H lane/claude-fix --fill",
		"gh pr create --head=claude/x --fill",
		"gh api repos/o/r/pulls -f head=claude/x -f title=t",
	} {
		got := UndercoverBashDecision(bashPayload(t, "s", dir, cmd))
		if got.Action != Block {
			t.Errorf("%q: Action = %v, want Block", cmd, got.Action)
			continue
		}
		if got.Policy != undercoverBashPolicy {
			t.Errorf("%q: Policy = %q, want %q", cmd, got.Policy, undercoverBashPolicy)
		}
		if !strings.Contains(got.Reason, "lane/<slug>") {
			t.Errorf("%q: Reason = %q, want the lane/<slug> fix", cmd, got.Reason)
		}
	}
}

func TestUndercoverBashDecision_RefusesTellTextForAPROrIssue(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	mustWrite(t, filepath.Join(dir, "body.md"), "Summary\n\n"+bashTellFooter+"\n")
	for _, cmd := range []string{
		`gh pr create --title "Fix the pager" --body "Summary. ` + bashTellFooter + `"`,
		`gh pr create --title "Ask Claude about it" --body b`,
		`gh pr create -t t -b "Co-Authored-By: Claude <noreply@anthropic.com>"`,
		`gh pr create --title=t --body="ran under opus-5"`,
		"gh pr create --title t --body-file body.md",
		"gh pr create -t t -F body.md",
		"gh pr create --title t --body-file=" + filepath.Join(dir, "body.md"),
		`gh pr edit 7 --body "sonnet-4 wrote it"`,
		`gh pr comment 7 --body "` + bashTellFooter + `"`,
		`gh pr review 7 --approve -b "anthropic tooling"`,
		`gh pr merge 7 --squash --subject "Fix the pager" --body "Co-Authored-By: Claude <noreply@anthropic.com>"`,
		`gh -R o/r issue create --title "claude leak" --body b`,
		`gh issue comment 5 -b "` + bashTellFooter + `"`,
		`gh issue edit 5 --title "opus-4 notes"`,
		`gh api repos/o/r/issues/5/comments -f body="` + bashTellFooter + `"`,
		`gh api repos/o/r/pulls -f title="claude fix" -f head=lane/x`,
		"gh api repos/o/r/issues/5/comments -F body=@body.md",
		"gh pr create --title t --body \"$(cat <<'EOF'\nSummary\n\n" + bashTellFooter + "\nEOF\n)\"",
		"gh pr create --title t --body-file - <<'EOF'\nSummary\n\n" + bashTellFooter + "\nEOF",
	} {
		got := UndercoverBashDecision(bashPayload(t, "s", dir, cmd))
		if got.Action != Block {
			t.Errorf("%q: Action = %v, want Block", cmd, got.Action)
			continue
		}
		if got.Policy != undercoverBashPolicy {
			t.Errorf("%q: Policy = %q, want %q", cmd, got.Policy, undercoverBashPolicy)
		}
		if !strings.Contains(got.Reason, "Rewrite the line") {
			t.Errorf("%q: Reason = %q, want the rewrite fix", cmd, got.Reason)
		}
	}
}

// A comment or review reaches GitHub by more routes than a flag: a JSON
// request file, and a GraphQL mutation whose text or variables carry it.
func TestUndercoverBashDecision_RefusesTellCommentsByEveryRoute(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	mustWrite(t, filepath.Join(dir, "comment.json"), `{"body": "Summary\n`+bashTellFooter+`"}`)
	mustWrite(t, filepath.Join(dir, "pr.json"), `{"title": "t", "head": "claude/x", "base": "main"}`)
	mustWrite(t, filepath.Join(dir, "body.md"), bashTellFooter+"\n")
	for _, cmd := range []string{
		"gh pr review 7 --comment -F body.md",
		"gh pr comment 7 --body-file body.md",
		"gh issue comment 5 --body-file=body.md",
		"gh api repos/o/r/issues/5/comments --input comment.json",
		"gh api -X POST repos/o/r/pulls/7/reviews --input=comment.json",
		"gh api repos/o/r/pulls --input pr.json",
		`gh api graphql -f query='mutation { addComment(input: {subjectId: "X", body: "ran under opus-5"}) { clientMutationId } }'`,
		`gh api graphql -f query='mutation($b: String!) { addComment(input: {subjectId: "X", body: $b}) { clientMutationId } }' -f b="` + bashTellFooter + `"`,
	} {
		if got := UndercoverBashDecision(bashPayload(t, "s", dir, cmd)); got.Action != Block {
			t.Errorf("%q: Action = %v, want Block", cmd, got.Action)
		}
	}
}

// A GraphQL query that only reads, and a JSON file without a tell, pass: a
// search for the word is not a post of it.
func TestUndercoverBashDecision_AllowsReadsAndOrdinaryRequestFiles(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	mustWrite(t, filepath.Join(dir, "ok.json"), `{"body": "Adds the agents doc to CLAUDE.md", "head": "lane/x"}`)
	for _, cmd := range []string{
		`gh api graphql -f query='query { search(query: "claude", type: ISSUE, first: 5) { issueCount } }'`,
		`gh api graphql -f q=claude -f query='query($q: String!) { search(query: $q, type: ISSUE, first: 5) { issueCount } }'`,
		"gh api repos/o/r/issues/5/comments --input ok.json",
		"gh api repos/o/r/issues/5/comments --input missing.json",
		"gh api repos/o/r/issues/5/comments --input body.txt",
	} {
		mustWrite(t, filepath.Join(dir, "body.txt"), "not json: claude")
		if got := UndercoverBashDecision(bashPayload(t, "s", dir, cmd)); got.Action == Block {
			t.Errorf("%q: blocked an ordinary command: %s", cmd, got.Reason)
		}
	}
}

// The refusal names what carried the tell and quotes the line.
func TestUndercoverBashDecision_RefusalQuotesTheFieldAndLine(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	got := UndercoverBashDecision(bashPayload(t, "s", dir, `gh pr create --title "Fix the pager" --body "Summary
`+bashTellFooter+`"`))
	for _, want := range []string{"PR or issue body, line 2", bashTellFooter} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("Reason = %q, want it to carry %q", got.Reason, want)
		}
	}
	got = UndercoverBashDecision(bashPayload(t, "s", dir, "git switch -c claude/x"))
	if !strings.Contains(got.Reason, `"claude/x"`) {
		t.Errorf("Reason = %q, want it to quote the branch name", got.Reason)
	}
}

func TestUndercoverBashDecision_AllowsOrdinaryNamesAndText(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	mustWrite(t, filepath.Join(dir, "body.md"), "Add the operating block to CLAUDE.md\n")
	for _, cmd := range []string{
		"git switch -c lane/cairo",
		"git branch lane/cursor-pagination main",
		"git branch -D claude/x",
		"git push origin --delete claude/x",
		"git push origin :claude/x",
		"git worktree add -b lane/agents-doc ../x main",
		"git log --oneline claude/x",
		`echo "git switch -c claude/x"`,
		"# git switch -c claude/x",
		"cat <<'EOF'\n" + bashTellFooter + "\nEOF",
		"cat > notes.md <<'EOF'\n" + bashTellFooter + "\nEOF",
		`gh pr create --title "Fix cursor pagination" --body "Adds the agents doc to CLAUDE.md"`,
		"gh pr create --title t --body-file body.md",
		"gh pr create --title t --body-file missing.md",
		"gh pr create --fill",
		"gh pr view 12",
		"gh pr list --head claude/x",
		"gh issue list --search claude",
		`gh api repos/o/r/pulls -H "Accept: application/json"`,
		`gh api repos/o/r/issues -f title="Shakespeare's Sonnet 18"`,
		"gh pr create --title t --body \"$(cat <<'EOF'\nSummary: Beethoven's Opus 131\nEOF\n)\"",
		// Only the heredoc body is text for gh; the command line around it
		// is not, so an echo beside the post is never judged as the body.
		"echo 'ask claude'; gh pr create -t t -F - <<'EOF'\nSummary\nEOF",
	} {
		if got := UndercoverBashDecision(bashPayload(t, "s", dir, cmd)); got.Action == Block {
			t.Errorf("%q: blocked an ordinary command: %s", cmd, got.Reason)
		}
	}
}

func TestUndercoverBashDecision_IsInertWhenTheRepoNeverOptedIn(t *testing.T) {
	dir := undercoverBashRepo(t, false)
	for _, cmd := range []string{
		"git switch -c claude/x",
		`gh pr create --title t --body "` + bashTellFooter + `"`,
	} {
		if got := UndercoverBashDecision(bashPayload(t, "s", dir, cmd)); got.Action == Block {
			t.Errorf("%q: blocked in a repo without undercover: %s", cmd, got.Reason)
		}
	}
	outside := t.TempDir()
	if got := UndercoverBashDecision(bashPayload(t, "s", outside, "git switch -c claude/x")); got.Action == Block {
		t.Errorf("blocked outside any repo: %s", got.Reason)
	}
}

func TestUndercoverBashDecision_RefusesThePowerShellToolAndIgnoresOthers(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	raw := string(bashPayload(t, "s", dir, "git switch -c claude/x"))
	if got := UndercoverBashDecision([]byte(strings.Replace(raw, `"Bash"`, `"PowerShell"`, 1))); got.Action != Block {
		t.Errorf("Action = %v, want Block for the PowerShell tool", got.Action)
	}
	if got := UndercoverBashDecision([]byte(strings.Replace(raw, `"Bash"`, `"Read"`, 1))); got.Action == Block {
		t.Errorf("blocked a tool that runs no command: %s", got.Reason)
	}
	if got := UndercoverBashDecision([]byte("{not json")); got.Action == Block {
		t.Errorf("blocked a payload it cannot parse: %s", got.Reason)
	}
}

// The body file is read against the command's cwd, and a file that cannot be
// read judges as no text: the wall fails open, as the PR wall does.
func TestUndercoverBashDecision_ReadsARelativeBodyFileFromCwd(t *testing.T) {
	dir := undercoverBashRepo(t, true)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(sub, "body.md"), bashTellFooter+"\n")
	if got := UndercoverBashDecision(bashPayload(t, "s", sub, "gh pr create -t t -F body.md")); got.Action != Block {
		t.Errorf("Action = %v, want Block for the body file under cwd", got.Action)
	}
	if got := UndercoverBashDecision(bashPayload(t, "s", dir, "gh pr create -t t -F body.md")); got.Action == Block {
		t.Errorf("blocked a body file that is not under cwd: %s", got.Reason)
	}
}
