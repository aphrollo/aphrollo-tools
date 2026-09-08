package tdd

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// verifyStub wires the three questions verify-closure asks gh. The diff is a
// real unified patch, because the check reads HUNKS now and not just a list
// of names.
func verifyStub(t *testing.T, prView, patch, issue string) {
	t.Helper()
	stubGhScript(t, map[string]string{
		"pr view":    prView,
		"pr diff":    patch,
		"issue view": issue,
	})
}

// stubGhFail makes the stub write to stderr and exit non-zero for one
// `<verb> <noun>` pair, which is how gh reports a missing label.
func stubGhFail(t *testing.T, call, message string) {
	t.Helper()
	verb, noun, _ := strings.Cut(call, " ")
	t.Setenv("GH_STUB_"+strings.ToUpper(verb)+"_"+strings.ToUpper(noun)+"_FAIL", message)
}

// escapeLabelled is an issue the loop owns, whose closes-by names no file.
const escapeLabelled = `{"labels":[{"name":"escape"}],"body":"closes-by: law | stage\n"}`

// diffFor is one file's entry in a unified patch, with lines given verbatim
// including their leading ' ', '+' or '-'.
func diffFor(path string, lines ...string) string {
	out := "diff --git a/" + path + " b/" + path + "\n" +
		"--- a/" + path + "\n+++ b/" + path + "\n@@ -1,3 +1,4 @@\n"
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

// The labels the loop opens issues under do not exist in a fresh repo, so
// `gh issue create --label escape` fails and every escape is recorded locally
// and never reaches anybody. The loop creates the label it needs.
func TestRecordEscapeCreatesItsLabelBeforeOpeningTheIssue(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGh(t, "https://github.com/o/r/issues/42")

	var out strings.Builder
	if _, err := RecordEscape(EscapeOptions{Reason: "a clippy warning reached main", Repo: repo}, &out); err != nil {
		t.Fatal(err)
	}
	argv := ghArgv(t, log)
	create := strings.Index(argv, "label create")
	issue := strings.Index(argv, "issue create")
	if create < 0 {
		t.Fatalf("the label was never created:\n%s", argv)
	}
	if issue < 0 || create > issue {
		t.Fatalf("the label must exist before the issue asks for it:\n%s", argv)
	}
	if !strings.Contains(argv, "label create "+EscapeKind) {
		t.Errorf("the label created is not the one the issue asks for:\n%s", argv)
	}
}

// A label that already exists must not turn the second escape into an error,
// so the create is idempotent.
func TestRecordEscapeSurvivesALabelThatAlreadyExists(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGh(t, "https://github.com/o/r/issues/42")
	stubGhFail(t, "label create", `label with name "escape" already exists`)

	var out strings.Builder
	r, err := RecordEscape(EscapeOptions{Reason: "second escape today", Repo: repo}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Number != 42 {
		t.Fatalf("an existing label must not stop the issue: %+v (%s)", r, out.String())
	}
}

// When gh refuses, its stderr is the only thing that says WHY. Dropping it
// tells the operator "no issue opened" and leaves them guessing between a
// missing label, no auth, and no network.
func TestRecordEscapeReportsWhatGhSaidWhenTheIssueFails(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGh(t, "")
	stubGhFail(t, "issue create", "HTTP 403: Resource not accessible by integration")

	var out strings.Builder
	r, err := RecordEscape(EscapeOptions{Reason: "still recorded", Repo: repo}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Issue != "" {
		t.Errorf("a failed create opens no issue, got %q", r.Issue)
	}
	if !strings.Contains(out.String(), "HTTP 403") {
		t.Fatalf("gh's own reason must reach the operator, got %q", out.String())
	}
	if len(readEscapes(t)) != 1 {
		t.Error("the local record must survive a failed create")
	}
}

// A PR that closes an escape must change a CHECK. Closing one with a sentence
// in a doc is how the count stops meaning anything.
func TestVerifyClosureRejectsAPRThatChangesNoCheck(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Fixes it.\n\nCloses #42\n","commits":[]}`,
		diffFor("docs/notes.md", "+a note"), escapeLabelled)

	var out strings.Builder
	ok, err := VerifyClosure(t.TempDir(), "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("a docs-only PR must not close an escape:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "#42") || !strings.Contains(out.String(), "FAIL") {
		t.Errorf("the verdict must name the issue and fail it:\n%s", out.String())
	}
}

func TestVerifyClosureAcceptsAPRThatTouchesALaw(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor(".ratchet/laws/nan_guard.toml", `+pattern = "x"`), escapeLabelled)

	var out strings.Builder
	ok, err := VerifyClosure(t.TempDir(), "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(out.String(), "ok") {
		t.Fatalf("a law change closes an escape:\n%s", out.String())
	}
}

// An issue nobody labelled is somebody else's issue; the check only judges
// the ones the loop owns.
func TestVerifyClosureIgnoresAnUnlabelledIssue(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #9\n","commits":[]}`,
		diffFor("README.md", "+a line"),
		`{"labels":[{"name":"bug"}],"body":"just a bug\n"}`)

	var out strings.Builder
	ok, err := VerifyClosure(t.TempDir(), "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("an unlabelled issue is out of scope:\n%s", out.String())
	}
}

// The gate stage that judges code is the CODE. A bare directory prefix let a
// PR touching only the spec markdown under internal/tdd close an escape,
// which is exactly the "a sentence in a document" case the loop forbids.
func TestVerifyClosureRejectsAGateDocWithNoGateChange(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/ratchet_laws.md", "+prose"), escapeLabelled)

	var out strings.Builder
	ok, _ := VerifyClosure(t.TempDir(), "31", &out)
	if ok {
		t.Fatalf("a doc under the gate package is not a gate stage:\n%s", out.String())
	}
}

// ratchet: test_removed TestVerifyClosureAcceptsAGateStageChange: it pinned
// the hole issue #292 fixes (an unnamed gate-stage change closed ANY
// escape); renamed and inverted below as
// TestVerifyClosure_RejectsAGateStageChangeNobodyNamed.
//
// A gate-stage change used to close ANY escape by the mere fact of touching
// internal/tdd or internal/ratchet, with no relation to the escape being
// closed at all — this pinned the hole (issue #292). Naming the stage on
// closes-by is now required for code exactly as it already was for a test.
func TestVerifyClosure_RejectsAGateStageChangeNobodyNamed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/precommit_go.go", "+	lint.Args = append(lint.Args, \"--strict\")"), escapeLabelled)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); ok {
		t.Fatalf("an unnamed gate stage change must not close an unrelated escape:\n%s", out.String())
	}
}

// Naming the stage on closes-by is what makes the SAME diff close the escape
// it actually names.
func TestVerifyClosure_AcceptsAGateStageChangeTheIssueNamed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/precommit_go.go", "+	lint.Args = append(lint.Args, \"--strict\")"),
		`{"labels":[{"name":"escape"}],"body":"closes-by: internal/tdd/precommit_go.go\n"}`)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); !ok {
		t.Fatalf("a gate stage change the issue named closes it:\n%s", out.String())
	}
}

// A comment reflowed onto the exact file an issue names must not close it:
// naming the right FILE is not the same as changing what it DOES.
func TestVerifyClosure_RejectsACommentOnlyEditToTheNamedFile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/precommit_go.go", "+	// re-flowed for clarity"),
		`{"labels":[{"name":"escape"}],"body":"closes-by: internal/tdd/precommit_go.go\n"}`)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); ok {
		t.Fatalf("a comment-only edit changes nothing a check depends on:\n%s", out.String())
	}
}

// A whitespace-only edit is the same hole by another route.
func TestVerifyClosure_RejectsAWhitespaceOnlyEditToTheNamedFile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/precommit_go.go", "+   "),
		`{"labels":[{"name":"escape"}],"body":"closes-by: internal/tdd/precommit_go.go\n"}`)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); ok {
		t.Fatalf("a whitespace-only edit changes nothing a check depends on:\n%s", out.String())
	}
}

// A test alone does not change what the gate CHECKS — unless the issue named
// it, which is what the closes-by line is for.
func TestVerifyClosureRejectsATestFileNobodyNamed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/escape_test.go", "+func TestX(t *testing.T) {}"), escapeLabelled)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); ok {
		t.Fatalf("an unnamed test is not a check change:\n%s", out.String())
	}
}

func TestVerifyClosureAcceptsATestTheIssueNamed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/escape_test.go", "+func TestX(t *testing.T) {}"),
		`{"labels":[{"name":"escape"}],"body":"closes-by: internal/tdd/escape_test.go\n"}`)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); !ok {
		t.Fatalf("a test the issue named is the fix it asked for:\n%s", out.String())
	}
}

// A closes-by line that names a DOC is the same escape hatch by another
// route, so the path it names has to be code.
func TestVerifyClosureRejectsADocNamedOnClosesBy(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("docs/notes.md", "+a note"),
		`{"labels":[{"name":"escape"}],"body":"closes-by: docs/notes.md\n"}`)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); ok {
		t.Fatalf("naming a doc must not close an escape:\n%s", out.String())
	}
}

// "Gate metadata" is the WORKSPACE manifest's aphrollo block. Accepting any
// crate's Cargo.toml meant a dependency bump closed escapes.
func TestVerifyClosureRejectsACrateManifest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("crates/a/Cargo.toml", "+serde = \"1\""), escapeLabelled)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); ok {
		t.Fatalf("a crate manifest is not gate metadata:\n%s", out.String())
	}
}

func TestVerifyClosureRejectsARootManifestChangedOutsideTheGateBlock(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("Cargo.toml", " [workspace.dependencies]", "+serde = \"1\""), escapeLabelled)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); ok {
		t.Fatalf("a dependency bump is not a gate change:\n%s", out.String())
	}
}

func TestVerifyClosureAcceptsTheGateBlockOfTheRootManifest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("Cargo.toml", " [workspace.metadata.aphrollo]", `+clippy-clean = ["server"]`), escapeLabelled)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); !ok {
		t.Fatalf("the aphrollo block IS the gate metadata:\n%s", out.String())
	}
}

// GitHub closes an issue named in a COMMIT message too, so a check that reads
// only the PR body lets a PR close an escape behind its back.
func TestVerifyClosureReadsClosesFromTheCommitMessagesToo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t,
		`{"body":"no mention here","commits":[{"messageHeadline":"tidy up","messageBody":"Closes #42"}]}`,
		diffFor("docs/notes.md", "+a note"), escapeLabelled)

	var out strings.Builder
	ok, _ := VerifyClosure(t.TempDir(), "31", &out)
	if ok || !strings.Contains(out.String(), "#42") {
		t.Fatalf("a commit message closes the issue just as the body does:\n%s", out.String())
	}
}

// An issue named in both the body and a commit is one issue, judged once.
func TestVerifyClosureJudgesAnIssueNamedTwiceOnlyOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t,
		`{"body":"Closes #42","commits":[{"messageHeadline":"x","messageBody":"Closes #42"}]}`,
		diffFor("docs/notes.md", "+a note"), escapeLabelled)

	var out strings.Builder
	VerifyClosure(t.TempDir(), "31", &out)
	if n := strings.Count(out.String(), "#42"); n != 1 {
		t.Fatalf("#42 judged %d times, want 1:\n%s", n, out.String())
	}
}

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
