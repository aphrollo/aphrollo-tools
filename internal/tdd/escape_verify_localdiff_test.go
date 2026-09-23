package tdd

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// A PR closing no escape/false-positive issue has nothing to verify, and
// verifying nothing needs no diff: #569 had VerifyClosure fetch the PR diff
// before it even knew whether the PR closed an escape at all, so a 147-file
// PR that closed no escape still asked gh for a diff GitHub refuses to hand
// back once it crosses 20000 lines.
func TestVerifyClosure_PRClosingNoEscapePassesWithoutFetchingTheDiff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	log := stubGhScript(t, map[string]string{
		"pr view":    `{"body":"Closes #9\n","commits":[]}`,
		"issue view": `{"labels":[{"name":"quality"}],"body":"just a quality issue\n"}`,
	})

	var out strings.Builder
	ok, err := VerifyClosure(t.TempDir(), "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("a PR closing no escape issue has nothing to fail:\n%s", out.String())
	}
	if argv := ghArgv(t, log); strings.Contains(argv, "pr diff") {
		t.Fatalf("pr diff must never be requested when the PR closes no escape:\n%s", argv)
	}
	if !strings.Contains(out.String(), "nothing to verify") {
		t.Fatalf("expected a line saying there is nothing to verify:\n%s", out.String())
	}
}

// When the PR DOES close an escape and gh refuses the diff for size, the
// verifier falls back to a local `git diff base...head` in repo rather than
// erroring out — the CI checkout that runs verify-closure has both refs.
func TestVerifyClosure_FallsBackToLocalDiffWhenGitHubRefusesTheSize(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	baseOut, err := gitRead(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(baseOut)

	write(t, repo, "pkg/widget_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {}\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "add widget test")
	headOut, err := gitRead(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(headOut)

	prView := fmt.Sprintf(`{"body":"Closes #42\n","commits":[],"baseRefOid":"%s","headRefOid":"%s"}`, base, head)
	issue := `{"labels":[{"name":"escape"}],"body":"closes-by: pkg/widget_test.go\n"}`
	verifyStub(t, prView, "unused: pr diff refuses below", issue)
	stubGhFail(t, "pr diff", "could not find pull request diff: HTTP 406: Sorry, the diff exceeded the maximum number of lines (20000) (https://api.github.com/repos/aphrollo/aphrollo-tools/pulls/568)\nPullRequest.diff too_large")

	var out strings.Builder
	ok, err := VerifyClosure(repo, "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("the local diff fallback must find the named test file and close the escape:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pkg/widget_test.go") {
		t.Fatalf("verdict must name the file the local diff found:\n%s", out.String())
	}
}

// The CI job's actions/checkout is a shallow depth-1 fetch of the merge ref
// (.github/workflows/pipeline.yml), so a checkout that runs verify-closure
// has NEITHER the PR's base nor its head commit as a reachable object —
// only the synthetic merge tip. `git diff base...head` in such a checkout
// fails with "unknown revision" on exactly the oversized-PR case the local
// fallback exists for, unless the fallback fetches those two shas by id
// (which GitHub serves) before diffing.
func TestVerifyClosure_LocalDiffFallbackFetchesTheRefsAShallowCloneLacks(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	origin := makeGoRepo(t) // one commit already: "base"
	baseOut, err := gitRead(origin, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(baseOut)

	write(t, origin, "pkg/widget_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {}\n")
	gitDo(t, origin, "add", ".")
	gitDo(t, origin, "commit", "-qm", "add widget test")
	headOut, err := gitRead(origin, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(headOut)

	// A third commit moves origin's tip past both base and head, so a
	// depth-1 clone of origin's current branch fetches neither — the same
	// shape as actions/checkout fetching the PR's merge ref rather than
	// either endpoint.
	write(t, origin, "tip.txt", "unrelated tip commit\n")
	gitDo(t, origin, "add", ".")
	gitDo(t, origin, "commit", "-qm", "advance past base and head")

	repo := filepath.Join(t.TempDir(), "repo")
	// A plain path clones same-disk repos via a local hardlink optimization
	// that ignores --depth entirely ("--depth is ignored in local clones");
	// a file:// URL forces the real pack-based transfer that actually
	// produces a shallow clone, same as GitHub's own smart-HTTP transport
	// does for actions/checkout.
	gitDo(t, t.TempDir(), "clone", "--depth", "1", fileURL(origin), repo)
	if commitExists(repo, base) || commitExists(repo, head) {
		t.Fatalf("test setup broken: the shallow clone must lack both base %s and head %s", base, head)
	}

	prView := fmt.Sprintf(`{"body":"Closes #42\n","commits":[],"baseRefOid":"%s","headRefOid":"%s"}`, base, head)
	issue := `{"labels":[{"name":"escape"}],"body":"closes-by: pkg/widget_test.go\n"}`
	verifyStub(t, prView, "unused: pr diff refuses below", issue)
	stubGhFail(t, "pr diff", "could not find pull request diff: HTTP 406: Sorry, the diff exceeded the maximum number of lines (20000) (https://api.github.com/repos/aphrollo/aphrollo-tools/pulls/568)\nPullRequest.diff too_large")

	var out strings.Builder
	ok, err := VerifyClosure(repo, "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("the fallback must fetch base and head by id before diffing them:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pkg/widget_test.go") {
		t.Fatalf("verdict must name the file found once the missing commits were fetched:\n%s", out.String())
	}
}

// fileURL turns a filesystem path into a file:// URL git will fetch over its
// real (pack-based) transport rather than the same-disk hardlink shortcut it
// takes for a bare path, which is what actually lets `--depth 1` bite.
func fileURL(path string) string {
	return "file:///" + filepath.ToSlash(path)
}

// A reason with multibyte characters must not be cut mid-rune: a byte slice
// puts a replacement character in the issue title.
func TestEscapeIssueTitleTruncatesOnRunes(t *testing.T) {
	title := escapeIssueTitle(EscapeRecord{Kind: EscapeKind, Reason: strings.Repeat("é", 120)})
	if !utf8.ValidString(title) {
		t.Fatalf("title is not valid UTF-8: %q", title)
	}
	if !strings.HasSuffix(title, "...") {
		t.Fatalf("a truncated title says so: %q", title)
	}
}
