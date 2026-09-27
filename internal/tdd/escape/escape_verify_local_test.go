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

// A closing keyword naming THIS repo's own issue by its fully-qualified form
// (owner/name#N, where owner/name is repo's own remote) must be recognized
// exactly as a bare #N would be — GitHub honours either spelling, and a
// branch that only ever spells it out in full must not slip past the
// escape-closure check for want of a keyword the parser does not yet
// understand (issue #931). makeGitHubRepo's own remote is o/r. Placed in the
// PR-body position (texts[0]).
func TestVerifyClosureLocal_QualifiedReferenceToThisRepoIsLocal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{"issue view": escapeLabelled})
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "docs/notes.md", "a note\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: a note")

	texts := []string{"Closes o/r#42", "docs: a note"}
	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, texts, base, "HEAD", &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("a docs-only branch must not close an escape named by its fully-qualified own-repo reference:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "#42") || !strings.Contains(out.String(), "FAIL") {
		t.Errorf("the verdict must name the issue and fail it:\n%s", out.String())
	}
}

// Same own-repo qualified reference, named in a COMMIT MESSAGE position (a
// later text) rather than the body — GitHub honours a closing keyword in
// either, so the fix must too.
func TestVerifyClosureLocal_QualifiedReferenceToThisRepoInACommitMessageIsLocal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{"issue view": escapeLabelled})
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "docs/notes.md", "a note\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: a note")

	texts := []string{"docs: a note", "fail-first on npm roots (aphrollo #928, closes o/r#42)"}
	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, texts, base, "HEAD", &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("a docs-only branch must not close an escape named by its fully-qualified own-repo reference in a commit message:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "#42") || !strings.Contains(out.String(), "FAIL") {
		t.Errorf("the verdict must name the issue and fail it:\n%s", out.String())
	}
}

// A closing keyword naming ANOTHER repo's issue (owner/name#N where
// owner/name is not repo's own remote) must never be read as a number in THIS
// repo's own tracker, and gh must never even be asked about it — GitHub
// resolves it against the named repo, not this one, and the escape-closure
// judgment has no business enforcing somebody else's escape against this
// branch's diff (issue #931). Placed in the PR-body position (texts[0]).
func TestVerifyClosureLocal_CrossRepoReferenceInTheBodyIsSkipped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGhScript(t, map[string]string{"issue view": escapeLabelled})
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "docs/notes.md", "a note\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: a note")

	texts := []string{"closes aphrollo/other#904", "docs: a note"}
	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, texts, base, "HEAD", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("a cross-repo reference must not be judged as this repo's escape:\n%s", out.String())
	}
	if strings.Contains(ghArgv(t, log), "904") {
		t.Errorf("a cross-repo #904 must never be looked up as a local issue:\n%s", ghArgv(t, log))
	}
	if !strings.Contains(out.String(), "aphrollo/other#904") {
		t.Errorf("expected a note naming the skipped cross-repo reference:\n%s", out.String())
	}
}

// Same cross-repo reference, named in a COMMIT MESSAGE position (a later
// text) rather than the body — GitHub honours a closing keyword in either, so
// the fix must too.
func TestVerifyClosureLocal_CrossRepoReferenceInACommitMessageIsSkipped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGhScript(t, map[string]string{"issue view": escapeLabelled})
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "docs/notes.md", "a note\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: a note")

	texts := []string{"docs: a note", "fail-first on npm roots (aphrollo #928, closes aphrollo/other#904)"}
	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, texts, base, "HEAD", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("a cross-repo reference in a commit message must not be judged as this repo's escape:\n%s", out.String())
	}
	if strings.Contains(ghArgv(t, log), "904") {
		t.Errorf("a cross-repo #904 must never be looked up as a local issue:\n%s", ghArgv(t, log))
	}
	if !strings.Contains(out.String(), "aphrollo/other#904") {
		t.Errorf("expected a note naming the skipped cross-repo reference:\n%s", out.String())
	}
}

// A LOCAL #N (bare, or owner/name#N naming THIS repo) that gh cannot resolve
// at all is a warning, not a hard failure: a number that names no local issue
// cannot be an unclosed local escape (issue #931). gh's own GraphQL refusal
// text is what tells this apart from a genuine gh failure, below.
func TestVerifyClosureLocal_UnresolvableLocalIssueIsAWarningNotAFailure(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGhFail(t, "issue view", "GraphQL: Could not resolve to an issue or pull request with the number of 904. (repository.issue)")
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "docs/notes.md", "a note\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: a note")

	var out strings.Builder
	ok, err := VerifyClosureLocal(repo, []string{"closes #904"}, base, "HEAD", &out)
	if err != nil {
		t.Fatalf("an issue number that does not resolve locally must warn, not fail: %v\n%s", err, out.String())
	}
	if !ok {
		t.Fatalf("a number that does not resolve locally cannot be an unclosed escape:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "warning") || !strings.Contains(out.String(), "904") {
		t.Errorf("expected a warning naming #904:\n%s", out.String())
	}
}

// A genuine gh failure (auth, network) on the SAME call must still refuse —
// only gh's specific "could not resolve" wording is read as absence, never a
// non-zero exit on its own.
func TestVerifyClosureLocal_ARealGhFailureOnIssueLookupStillRefuses(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGhFail(t, "issue view", "HTTP 401: Bad credentials (https://api.github.com/graphql)")
	base := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "docs/notes.md", "a note\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "docs: a note")

	if _, err := VerifyClosureLocal(repo, []string{"closes #904"}, base, "HEAD", &strings.Builder{}); err == nil {
		t.Fatal("a genuine gh failure (auth) must still refuse, not be read as an unresolved issue")
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
