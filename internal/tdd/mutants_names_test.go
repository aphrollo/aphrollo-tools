package tdd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every line here is pasted verbatim from a real cargo-mutants 27.1.0 run
// under /d/Projects/.worktrees/borld/*/mutants.out. The shape they prove is
// `<file>:<line>:<col>: <mutation>` — with the COLUMN, which the first parser
// dropped.
var realCaughtLines = []string{
	"crates/editor_client/src/creator.rs:101:5: replace send_undo_redo with ()",
	"crates/editor_client/src/creator.rs:101:33: replace || with && in send_undo_redo",
	"crates/editor_client/src/creator.rs:101:16: replace || with && in send_undo_redo",
	"crates/forge/src/tire/attach.rs:28:5: replace attach_tire -> Result<usize, BuildError> with Ok(0)",
}

var realMissedLines = []string{
	"crates/editor_server/src/history.rs:181:58: replace > with >= in LastEditSeq::superseding_client",
}

// The exclusion has to be the tool's own spelling or it excludes nothing. The
// first version rebuilt the name as `<file>:<line>: <mutation>`, dropping the
// column cargo-mutants prints, so `--exclude-re` never matched and a resumed
// run re-measured all of it.
func TestMutantNames_AreTheToolsOwnLineVerbatim(t *testing.T) {
	for _, line := range realCaughtLines {
		m, ok := parseMutantLine(line)
		if !ok {
			t.Fatalf("could not parse a real mutants.out line: %q", line)
		}
		if got := mutantNames([]MutantOutcome{m}); len(got) != 1 || got[0] != line {
			t.Fatalf("name = %q, want the line verbatim: %q", got, line)
		}
	}
}

// The column is part of the identity, not decoration: this file's two `||`
// mutants sit on the same line with identical text and differ only by column.
func TestParseMutantLine_KeepsTheColumnThatTellsTwoMutantsApart(t *testing.T) {
	a, okA := parseMutantLine(realCaughtLines[1])
	b, okB := parseMutantLine(realCaughtLines[2])
	if !okA || !okB {
		t.Fatal("both real lines must parse")
	}
	if a.Col != 33 || b.Col != 16 {
		t.Fatalf("columns = %d and %d, want 33 and 16", a.Col, b.Col)
	}
	if a.key() == b.key() {
		t.Fatalf("two distinct mutants share a key: %+v", a.key())
	}
}

// The bug this closes: one verdict overwriting another. Both of these are
// caught in the real run, but a store keyed without the column keeps one
// entry, so a lane where one of them is MISSED reports the other's verdict.
func TestMutantStore_KeepsBothMutantsOnOneLineApart(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a, _ := parseMutantLine(realCaughtLines[1])
	b, _ := parseMutantLine(realCaughtLines[2])
	a.Status, b.Status = "caught", "missed"
	for _, m := range []*MutantOutcome{&a, &b} {
		m.Package, m.Blob, m.Fence = "crates/editor_client", "blobC", "fenceC"
	}
	MergeMutantStore("borld", []MutantOutcome{a, b})

	store := LoadMutantStore("borld")
	if len(store) != 2 {
		t.Fatalf("store holds %d entries, want both columns: %+v", len(store), store)
	}
	if got := store[b.key()].Status; got != "missed" {
		t.Fatalf("the missed mutant reads as %q — a verdict was overwritten", got)
	}
}

// A resumed run must exclude what it already judged, and the exclusion is the
// verbatim line anchored: cargo-mutants matches --exclude-re against exactly
// the string it prints.
func TestMutantsArgv_ExcludesTheVerbatimLineAnchored(t *testing.T) {
	m, _ := parseMutantLine(realCaughtLines[3])
	argv := MutantsArgv("d.diff", false, mutantNames([]MutantOutcome{m}))

	want := "^" + regexp.QuoteMeta(realCaughtLines[3]) + "$"
	found := false
	for i, a := range argv {
		if a == "--exclude-re" && i+1 < len(argv) && argv[i+1] == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("argv = %v\nwant an --exclude-re of %q", argv, want)
	}
}

// A few thousand judged mutants is an ordinary resumed run on this workspace,
// and every exclusion rides in one environment variable: Windows caps the
// whole block at 32,767 characters, so an unbounded list silently truncates
// the run's own arguments.
func TestMutantsArgv_StaysInsideTheEnvironmentBlockLimit(t *testing.T) {
	var judged []string
	for _, line := range realCaughtLines {
		for i := 0; i < 2000; i++ {
			judged = append(judged, line)
		}
	}
	argv := MutantsArgv("d.diff", false, judged)
	size := len(strings.Join(argv, " "))
	if size > mutantsArgvBudget {
		t.Fatalf("argv is %d chars, over the %d budget: the environment block would truncate", size, mutantsArgvBudget)
	}
	// Bounded, but not empty: dropping an exclusion only re-measures that one
	// mutant, so the run keeps as many as fit.
	if !strings.Contains(strings.Join(argv, " "), "--exclude-re") {
		t.Fatal("every exclusion was dropped; the resume then measures the whole diff again")
	}
}

// readMutantsOut reads the real files, with the real names.
func TestReadMutantsOut_ReadsRealVerdictFilesWithTheirColumns(t *testing.T) {
	wt := t.TempDir()
	out := filepath.Join(wt, "mutants.out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, lines []string) {
		if err := os.WriteFile(filepath.Join(out, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("caught.txt", realCaughtLines)
	write("missed.txt", realMissedLines)

	got := readMutantsOut(wt)
	if len(got) != len(realCaughtLines)+len(realMissedLines) {
		t.Fatalf("read %d verdicts, want %d", len(got), len(realCaughtLines)+len(realMissedLines))
	}
	for _, m := range got {
		if m.Col == 0 || m.Name == "" {
			t.Fatalf("outcome %+v lost the column or the tool's own name", m)
		}
	}
}
