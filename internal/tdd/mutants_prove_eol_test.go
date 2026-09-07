package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// staleCRLFFixture reproduces issue #519's exact real-git shape: a file
// committed with CRLF bytes before `.gitattributes` existed, then
// `git add --renormalize` brings the INDEX to the declared eol=lf without
// ever touching the checked-out file — exactly what `core.autocrlf=input`
// does on every real checkout (converts on commit, never on checkout).
// `git status` reads clean throughout, which is why nothing an operator
// looks at shows the drift.
func staleCRLFFixture(t *testing.T) (root, rel string) {
	t.Helper()
	root = makeGoRepo(t)
	gitDo(t, root, "config", "core.autocrlf", "false")

	rel = "stale.go"
	if err := os.WriteFile(filepath.Join(root, rel), []byte("package m\r\n\r\nfunc Stale() {}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, root, "add", rel)
	gitDo(t, root, "commit", "-qm", "stale crlf, pre-attributes")

	write(t, root, ".gitattributes", "* text=auto eol=lf\n")
	gitDo(t, root, "add", ".gitattributes")
	gitDo(t, root, "add", "--renormalize", ".")
	gitDo(t, root, "commit", "-qm", "adopt gitattributes")

	if status, err := git(root, "status", "--short"); err != nil || strings.TrimSpace(status) != "" {
		t.Fatalf("fixture setup left a dirty tree (should read clean, matching the report): %q (err %v)", status, err)
	}
	return root, rel
}

func TestEolDriftNote_NamesTheMismatchOnStaleCRLFBytes(t *testing.T) {
	root, rel := staleCRLFFixture(t)

	note := eolDriftNote(root, rel)
	if note == "" {
		t.Fatal("eolDriftNote found nothing for a file with stale CRLF bytes against a declared eol=lf attribute")
	}
	if !strings.Contains(note, rel) || !strings.Contains(note, "crlf") || !strings.Contains(note, "lf") {
		t.Fatalf("note does not name the file and both line-ending values: %q", note)
	}
	if !strings.Contains(note, "re-checkout") {
		t.Fatalf("note never names the actual remedy (re-checkout): %q", note)
	}
}

func TestEolDriftNote_EmptyWhenTheWorktreeAgreesWithTheDeclaredAttribute(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, ".gitattributes", "* text=auto eol=lf\n")
	write(t, root, "clean.go", "package m\n\nfunc Clean() {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "clean")

	if note := eolDriftNote(root, "clean.go"); note != "" {
		t.Fatalf("eolDriftNote(%q) = %q, want empty — the worktree already agrees with eol=lf", "clean.go", note)
	}
}

// The exact false-survivor setup from issue #519: a --old pattern authored
// against LF bytes never matches a file that is CRLF on disk, and the
// refusal that follows now names the drift as a likely cause rather than
// leaving the operator to rediscover it by hand.
func TestRunMutantsProve_RefusalNamesEolDriftOnAStaleCRLFFile(t *testing.T) {
	root, rel := staleCRLFFixture(t)

	var out, errb strings.Builder
	code := RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(root, rel),
		// authored against LF newlines; the real bytes on disk are CRLF, so
		// this multi-line pattern matches nothing even though the visible
		// text is identical — exactly the shape issue #519 reports.
		Old:      "package m\n\nfunc Stale() {}\n",
		New:      "package m\n\nfunc Stale() { panic(\"mutated\") }\n",
		WantFail: "TestNothing",
	}, func(r Runner, root string) SuiteResult {
		t.Fatal("the suite ran despite the mutation pattern matching nothing")
		return SuiteResult{}
	}, &out, &errb)

	if code != ExitMutantsProveRefused {
		t.Fatalf("exit = %d, want ExitMutantsProveRefused\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "crlf") {
		t.Fatalf("refusal never mentions the stale CRLF bytes: %q", errb.String())
	}
}
