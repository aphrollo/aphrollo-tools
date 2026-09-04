package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A touched path git cannot resolve at its staged location (deleted, renamed
// away, or simply never staged) must not block the commit and must not
// silently vanish either: it is logged and counted once, the same shape as
// an absent linter.
func TestGoFmtStage_LogsAndCountsAStagedPathGitCannotResolve(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)

	res := goFmtStage("test", root, root, []string{"nonexistent.go"})
	if res.Blocked {
		t.Fatalf("a path git cannot resolve must not block: %s", res.Message)
	}
	requireLoggedVerdict(t, cfg, "gofmt-index-unreadable")
}

// The unreadable count is a REPORT of a systematic miss, so it must stay
// silent when nothing was missed: a run where every staged path resolves
// writes no "could not read" line and no gofmt-index-unreadable verdict.
func TestGoFmtStage_SaysNothingWhenEveryStagedPathResolves(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := goFmtStage("test", root, root, []string{"widget.go"})
	if res.Blocked {
		t.Fatalf("gofmt-clean staged source must not block: %s", res.Message)
	}
	// An absent log is the strongest form of the same claim: nothing was
	// reported at all, so read it directly rather than through the helper
	// that requires the file to exist.
	logged, _ := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	for line := range strings.SplitSeq(string(logged), "\n") {
		if e, ok := parseGateLine(line); ok && e.verdict == "gofmt-index-unreadable" {
			t.Fatalf("no path was unreadable, yet the stage reported one: %s", line)
		}
	}
}

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
func TestPrecommitGofmt_RejectsUnformattedCRLFStagedGo(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, false)
	write(t, root, "widget.go", "package m\r\n\r\nfunc Widget() int { return 1 }\r\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "gofmt") {
		t.Fatalf("a CRLF-staged file is not gofmt-clean, got %+v", res)
	}
}

// The stage must judge what a commit would actually carry (the index), never
// whatever the working tree happens to hold when precommit runs: a clean
// STAGED blob must not block even though the file on disk was rewritten
// unformatted/CRLF afterward without being re-added.
func TestPrecommitGofmt_IgnoresAnUnformattedWorkingTreeWhenTheIndexIsClean(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, false)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	// Rewritten after staging, never re-added: the index still holds the
	// clean content this commit would actually carry.
	write(t, root, "widget.go", "package m\r\n\r\nfunc Widget() int {\nreturn 1\n}\r\n")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("a clean STAGED blob must not block over a dirty working tree: %s", res.Message)
	}
}

// The reverse of the above: an unformatted/CRLF blob actually staged must
// block even when the working tree was cleaned up afterward without being
// re-added — otherwise the stage would be judging the wrong copy of the
// file.
func TestPrecommitGofmt_RejectsAnUnformattedIndexEvenWhenTheWorkingTreeWasCleanedUp(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, false)
	write(t, root, "widget.go", "package m\r\n\r\nfunc Widget() int {\nreturn 1\n}\r\n")
	gitDo(t, root, "add", ".")
	// Cleaned up after staging, never re-added: the index still holds the
	// unformatted/CRLF content this commit would carry.
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "gofmt") {
		t.Fatalf("an unformatted STAGED blob must block even with a clean working tree, got %+v", res)
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

// A monorepo's Go root can sit below the repo's git top level: git's bare
// `:path` form resolves from the repo TOP regardless of cwd, so feeding it a
// Go-root-relative path looks up the wrong location, errors, and (the bug)
// that error was swallowed as "nothing staged to judge" — gofmt then reports
// clean over a file it never read.
func TestPrecommitGofmt_JudgesTheIndexBlobForANestedGoRoot(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, false)
	write(t, root, "sub/go.mod", "module sub\n\ngo 1.26\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "add nested module")

	write(t, root, "sub/widget.go", "package m\n\nfunc Widget() int {\nreturn 1\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "gofmt") {
		t.Fatalf("unformatted staged content in a nested Go root must be rejected, got %+v", res)
	}
}
