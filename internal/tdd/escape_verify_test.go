package tdd

import (
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

func TestVerifyClosureAcceptsAGateStageChange(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor("internal/tdd/precommit_go.go", "+	lint.Args = append(lint.Args, \"--strict\")"), escapeLabelled)

	var out strings.Builder
	if ok, _ := VerifyClosure(t.TempDir(), "31", &out); !ok {
		t.Fatalf("a gate stage change closes an escape:\n%s", out.String())
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
