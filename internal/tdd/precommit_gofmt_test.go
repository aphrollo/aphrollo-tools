package tdd

import (
	"strings"
	"testing"
)

// Ungofmt'd staged Go is rejected before vet ever runs — cheaper than a
// build, and it saves the round trip through CI for a space.
func TestPrecommitGofmt_RejectsUnformattedStagedGo(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, false)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int {\nreturn 1\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "gofmt") {
		t.Fatalf("expected a gofmt rejection, got %+v", res)
	}
	if !strings.Contains(res.Message, "widget.go") {
		t.Errorf("message %q does not name the offending file", res.Message)
	}
}

// The whole point: a CRLF-line-ended file is never gofmt-clean (gofmt's
// canonical output is always LF), so a checkout that predates
// .gitattributes and still holds CRLF in its staged blob must be caught,
// not waved through because the file "looks fine" in an editor that hides
// line endings.
func TestPrecommitGofmt_JudgesTheIndexBlobNotTheWorkingTree(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, false)
	write(t, root, "widget.go", "package m\r\n\r\nfunc Widget() int { return 1 }\r\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "gofmt") {
		t.Fatalf("a CRLF-staged file is not gofmt-clean, got %+v", res)
	}
}

// A clean, already-gofmt'd file must never block.
func TestPrecommitGofmt_AllowsAlreadyFormattedGo(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, false)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("gofmt-clean source must not block: %s", res.Message)
	}
}
