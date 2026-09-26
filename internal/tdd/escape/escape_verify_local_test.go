package escape

import (
	"strings"
	"testing"
)

// VerifyClosureLocal is VerifyClosure's pre-PR half (workspace pr/submit/ship
// call it before gh has ever heard of the branch), so these tests drive it
// against a real local repo and a real local diff rather than a PR patch.

// A branch whose commit messages close no issue has nothing to verify, and
// needs no gh issue lookup at all.
func TestVerifyClosureLocal_NothingToVerifyWhenNoIssueIsClosed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "README.md", "unrelated change\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: unrelated change")

	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, []string{"docs: unrelated change"}, base, "HEAD", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("nothing closed, nothing to fail:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "nothing to verify") {
		t.Errorf("expected a line saying there is nothing to verify:\n%s", out.String())
	}
}

// The branch closes an escape but its local diff never touches a check — the
// same refusal VerifyClosure gives a PR, given before the PR exists.
func TestVerifyClosureLocal_RejectsABranchThatChangesNoCheck(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stubGhScript(t, map[string]string{
		"issue view": escapeLabelled,
	})
	repo := makeGoRepo(t)
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "docs/notes.md", "a note\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: a note")

	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, []string{"Closes #42"}, base, "HEAD", &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("a docs-only branch must not close an escape:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "#42") || !strings.Contains(out.String(), "FAIL") {
		t.Errorf("the verdict must name the issue and fail it:\n%s", out.String())
	}
}

// A branch that DOES change a law closes the escape it names, exactly as
// VerifyClosure accepts the same diff once it is a PR.
func TestVerifyClosureLocal_AcceptsABranchThatTouchesALaw(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stubGhScript(t, map[string]string{
		"issue view": escapeLabelled,
	})
	repo := makeGoRepo(t)
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, ".ratchet/laws/nan_guard.toml", `pattern = "x"`+"\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "narrow the nan_guard law\n\nCloses #42")

	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, []string{"narrow the nan_guard law", "Closes #42"}, base, "HEAD", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(out.String(), "ok") {
		t.Fatalf("a law change closes an escape:\n%s", out.String())
	}
}

// mustGit runs one git command in dir and fails the test on error, returning
// stdout — a thin wrapper so these tests can read a rev without a bespoke
// error-handling block at every call site.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitRead(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}
