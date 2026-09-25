package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #871: a PR opened with a raw `gh pr create` never passes through
// `aphrollo workspace pr`, so the pre-PR mutation measurement that verb runs
// never ran and five survivors reached CI. With mutants-before-pr declared,
// the Bash/PowerShell hook refuses a command that opens a PR directly.

// prRepo is a git repo whose aphrollo.toml carries the given [aphrollo] body.
func prRepo(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "aphrollo.toml"), []byte("[aphrollo]\n"+config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDirectPROpenDecision_RefusesEachFormThatOpensAPR(t *testing.T) {
	dir := prRepo(t, "mutants-before-pr = true\n")
	for _, cmd := range []string{
		"gh pr create --fill",
		"gh pr create",
		"gh pr new --title t --body b",
		"/usr/bin/gh pr create --draft",
		`gh pr create --title "a title" --body "x"`,
		"git push -u origin lane/x && gh pr create --fill",
		"cd sub; gh pr create --fill",
		"true | gh pr create --fill",
		"gh -R owner/repo pr create --fill",
		"gh --repo owner/repo pr create --fill",
		"gh --repo=owner/repo pr create --fill",
		"gh pr -R owner/repo create --fill",
		`bash -c "gh pr create --fill"`,
		"echo $(gh pr create --fill)",
		"gh api -X POST repos/o/r/pulls -f title=t -f head=h -f base=main",
		"gh api repos/o/r/pulls -X POST",
		"gh api --method POST /repos/o/r/pulls --input body.json",
		"gh api --method=post repos/o/r/pulls",
		"gh api -XPOST repos/{owner}/{repo}/pulls",
		"gh api repos/o/r/pulls -f title=t -f head=h -f base=main",
		"gh api repos/o/r/pulls -ftitle=t",
		"gh api repos/o/r/pulls --field title=t",
		"gh api repos/o/r/pulls --raw-field=title=t",
		"gh api repos/o/r/pulls -F draft=true",
		`gh api "repos/o/r/pulls/" --input -`,
	} {
		got := DirectPROpenDecision(bashPayload(t, "s", dir, cmd))
		if got.Action != Block {
			t.Errorf("%q: Action = %v, want Block", cmd, got.Action)
			continue
		}
		if got.Policy != directPROpenPolicy {
			t.Errorf("%q: Policy = %q, want %q", cmd, got.Policy, directPROpenPolicy)
		}
		if !strings.Contains(got.Reason, "aphrollo workspace pr") {
			t.Errorf("%q: Reason = %q, want it to name `aphrollo workspace pr`", cmd, got.Reason)
		}
	}
}

func TestDirectPROpenDecision_RefusesThePowerShellTool(t *testing.T) {
	dir := prRepo(t, "mutants-before-pr = true\n")
	raw := []byte(strings.Replace(string(bashPayload(t, "s", dir, "gh pr create --fill")), `"Bash"`, `"PowerShell"`, 1))
	if got := DirectPROpenDecision(raw); got.Action != Block {
		t.Fatalf("Action = %v, want Block for the PowerShell tool", got.Action)
	}
}

func TestDirectPROpenDecision_AllowsWhatOpensNoPR(t *testing.T) {
	dir := prRepo(t, "mutants-before-pr = true\n")
	for _, cmd := range []string{
		"gh pr view 12",
		"gh pr list --state open",
		"gh pr",
		"gh",
		"gh -R owner/repo pr view create",
		"gh pr view --json title create",
		"gh issue create --title t",
		"tea pr create --title t",
		`bash -c "gh pr view 1"`,
		"echo $(gh pr view 1 --json url)",
		"aphrollo workspace pr --title t --body b",
		`echo "gh pr create --fill"`,
		`git commit -m "gh pr create was not used"`,
		"# gh pr create --fill",
		"gh pr view 3 # then gh pr create",
		"gh api repos/o/r/pulls",
		"gh api -X GET repos/o/r/pulls -f state=open",
		"gh api repos/o/r/pulls/12/reviews -f event=APPROVE",
		"gh api -X POST repos/o/r/issues -f title=t",
		"gh api -X POST repos/o/r/pulls/12/comments",
		"gh api --method POST",
		"-X POST repos/o/r/pulls",
	} {
		if got := DirectPROpenDecision(bashPayload(t, "s", dir, cmd)); got.Action != Allow {
			t.Errorf("%q: Action = %v, want Allow; Reason=%q", cmd, got.Action, got.Reason)
		}
	}
}

func TestDirectPROpenDecision_InertWithoutTheKey(t *testing.T) {
	for name, config := range map[string]string{
		"off":    "mutants-before-pr = false\n",
		"absent": "undercover = true\n",
	} {
		dir := prRepo(t, config)
		if got := DirectPROpenDecision(bashPayload(t, "s", dir, "gh pr create --fill")); got.Action != Allow {
			t.Errorf("%s: Action = %v, want Allow when mutants-before-pr is not declared", name, got.Action)
		}
	}
}

func TestDirectPROpenDecision_InertOutsideAGitRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aphrollo.toml"), []byte("[aphrollo]\nmutants-before-pr = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DirectPROpenDecision(bashPayload(t, "s", dir, "gh pr create --fill")); got.Action != Allow {
		t.Fatalf("Action = %v, want Allow outside a git repo", got.Action)
	}
}

func TestDirectPROpenDecision_IgnoresNonShellTools(t *testing.T) {
	dir := prRepo(t, "mutants-before-pr = true\n")
	raw := []byte(strings.Replace(string(bashPayload(t, "s", dir, "gh pr create --fill")), `"Bash"`, `"Edit"`, 1))
	if got := DirectPROpenDecision(raw); got.Action != Allow {
		t.Fatalf("Action = %v, want Allow for a non-shell tool", got.Action)
	}
	if got := DirectPROpenDecision([]byte("{bad")); got.Action != Allow {
		t.Fatalf("Action = %v, want Allow for an unparseable payload", got.Action)
	}
}

// A config the gate cannot read must not turn the wall inert: every other
// reader of the mutants config refuses on it, and so does this one.
func TestDirectPROpenDecision_RefusesOnAnUnreadableMutantsConfig(t *testing.T) {
	dir := prRepo(t, "mutants-before-pr = true\nmutants-local = true\n")
	got := DirectPROpenDecision(bashPayload(t, "s", dir, "gh pr create --fill"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block when the mutants config is broken", got.Action)
	}
	if !strings.Contains(got.Reason, "mutants-local") {
		t.Errorf("Reason = %q, want it to name the config problem (the retired key)", got.Reason)
	}
	if got := DirectPROpenDecision(bashPayload(t, "s", dir, "gh pr view 1")); got.Action != Allow {
		t.Errorf("gh pr view: Action = %v, want Allow — a broken config refuses only a command that opens a PR", got.Action)
	}
}

// Issue #884: the GraphQL API opens a PR too, through a createPullRequest
// mutation, sent inline or read from a file by gh's `-F query=@file`.
const createPRMutation = `mutation { createPullRequest(input: {repositoryId: "R", baseRefName: "main", headRefName: "x", title: "t"}) { pullRequest { url } } }`

func TestDirectPROpenDecision_RefusesACreatePullRequestMutation(t *testing.T) {
	dir := prRepo(t, "mutants-before-pr = true\n")
	if err := os.WriteFile(filepath.Join(dir, "q.graphql"), []byte("# opens the PR\n"+createPRMutation+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"gh api graphql -f query='" + createPRMutation + "'",
		"gh api graphql -F query='" + createPRMutation + "'",
		"gh api graphql --field query='" + createPRMutation + "'",
		"gh api graphql --raw-field=query='" + createPRMutation + "'",
		"gh api graphql -fquery='" + createPRMutation + "'",
		"gh api -f query='" + createPRMutation + "' graphql",
		"gh api graphql -f query='mutation($i: CreatePullRequestInput!) { pr: createPullRequest (input: $i) { clientMutationId } }' -f i=x",
		"gh api graphql -F query=@q.graphql",
		"gh api graphql --field=query=@q.graphql",
		"gh api graphql -F query=@" + filepath.Join(dir, "q.graphql"),
	} {
		got := DirectPROpenDecision(bashPayload(t, "s", dir, cmd))
		if got.Action != Block {
			t.Errorf("%q: Action = %v, want Block", cmd, got.Action)
			continue
		}
		if !strings.Contains(got.Reason, "aphrollo workspace pr") {
			t.Errorf("%q: Reason = %q, want it to name `aphrollo workspace pr`", cmd, got.Reason)
		}
	}
}

func TestDirectPROpenDecision_AllowsGraphQLThatOpensNoPR(t *testing.T) {
	dir := prRepo(t, "mutants-before-pr = true\n")
	if err := os.WriteFile(filepath.Join(dir, "q.graphql"), []byte(createPRMutation), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"gh api graphql -f query='query { viewer { login } }'",
		`gh api graphql -f query='query { repository(owner: "o", name: "r") { pullRequests(first: 1) { nodes { id } } } }'`,
		`gh api graphql -f query='mutation { addComment(input: {subjectId: "x", body: "createPullRequest(y)"}) { clientMutationId } }'`,
		`gh api graphql -f query='mutation { addComment(input: {subjectId: "x", body: """a createPullRequest(y) "note" here"""}) { clientMutationId } }'`,
		"gh api graphql -f query='mutation { addComment(input: {subjectId: \"x\", body: \"b\"}) { clientMutationId } # not createPullRequest(\n}'",
		"gh api graphql -f query=@q.graphql",
		"gh api graphql -F query=@missing.graphql",
		"gh api graphql -F query=@-",
		"gh api graphql -H 'query=" + createPRMutation + "' -f query='query { viewer { login } }'",
		"gh api graphql -f note='" + createPRMutation + "' -f query='query { viewer { login } }'",
		"gh api -X GET repos/o/r/issues -f query='" + createPRMutation + "'",
	} {
		if got := DirectPROpenDecision(bashPayload(t, "s", dir, cmd)); got.Action != Allow {
			t.Errorf("%q: Action = %v, want Allow; Reason=%q", cmd, got.Action, got.Reason)
		}
	}
}
