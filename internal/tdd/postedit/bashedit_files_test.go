package postedit

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLiveSourceFiles_DropsWhatTheCommandDeleted(t *testing.T) {
	root := t.TempDir()
	write(t, root, "kept.go", "package m\n")

	got := liveSourceFiles(root, []string{"gone.go", "kept.go", "sub/also-gone.go"})

	if want := []string{filepath.Join(root, "kept.go")}; !slices.Equal(got, want) {
		t.Fatalf("live = %v, want %v", got, want)
	}
}

func TestRecordBashEdits_OneLedgerPerProjectRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	gitRoot := t.TempDir()
	write(t, gitRoot, "svcA/go.mod", "module example.com/svcA\n\ngo 1.26\n")
	write(t, gitRoot, "svcB/go.mod", "module example.com/svcB\n\ngo 1.26\n")
	write(t, gitRoot, "svcA/a.go", "package main\n")
	write(t, gitRoot, "svcA/a2.go", "package main\n")
	write(t, gitRoot, "svcB/b.go", "package main\n")
	gitInit(t, gitRoot)
	gitDo(t, gitRoot, "add", "-A")
	gitDo(t, gitRoot, "commit", "-qm", "base")
	a, a2, b := filepath.Join(gitRoot, "svcA", "a.go"), filepath.Join(gitRoot, "svcA", "a2.go"), filepath.Join(gitRoot, "svcB", "b.go")

	ids := recordBashEdits([]string{a, b, a2})

	rootA, rootB := filepath.Join(gitRoot, "svcA"), filepath.Join(gitRoot, "svcB")
	if len(ids) != 2 || len(strings.Split(ids[rootA], ",")) != 2 || len(strings.Split(ids[rootB], ",")) != 1 {
		t.Fatalf("ids = %v, want two for svcA and one for svcB", ids)
	}
	if rows := ledgerRows(t, rootA); len(rows) != 2 || rows[0].file != "a.go" || rows[1].file != "a2.go" {
		t.Errorf("svcA's ledger = %+v, want a.go and a2.go", rows)
	}
	if rows := ledgerRows(t, rootB); len(rows) != 1 || rows[0].file != "b.go" {
		t.Errorf("svcB's ledger = %+v, want b.go", rows)
	}
}

func TestRecordBashEdits_AFileInNoProjectRecordsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "loose.go", "package m\n")

	if ids := recordBashEdits([]string{filepath.Join(dir, "loose.go")}); len(ids) != 0 {
		t.Fatalf("ids = %v, want none", ids)
	}
}

func TestGofmtEditedAll_NamesEveryFileItFormattedAndNoOther(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "messy1.go", "package m\n\nfunc  A()  {}\n")
	write(t, dir, "clean.go", "package m\n\nfunc B() {}\n")
	write(t, dir, "messy2.go", "package m\n\nfunc  C()  {}\n")
	files := []string{filepath.Join(dir, "messy1.go"), filepath.Join(dir, "clean.go"), filepath.Join(dir, "messy2.go")}

	got := gofmtEditedAll(files)

	want := "gofmt formatted " + files[0] + ", " + files[2]
	if got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if again := gofmtEditedAll(files); again != "" {
		t.Fatalf("formatted files need no more formatting, got %q", again)
	}
}

// ratchet: test_removed TestLintPackages_GroupsGoFilesByDirectoryInFirstSeenOrder: the edit-time lint moved into the run (lintrun.go), so its package grouping is gone
// ratchet: test_removed TestLintEditedFiles_RunsOncePerPackageAndNamesEachFilesFindings: the edit-time lint moved into the run (lintrun.go)
// ratchet: test_removed TestLintEditedFiles_ATimeoutIsTheNoteAndStopsTheRest: the edit-time lint moved into the run (lintrun.go)
// ratchet: test_removed TestLintEditedFiles_AFileWithNoChangedLinesIsNotLinted: the edit-time lint moved into the run (lintrun.go)

func TestBashSmellLines_ABlockingVerdictSaysAnEditWouldHaveBeenDenied(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	mustWrite(t, filepath.Join(root, "slow_test.go"), parityWidgetTest)

	lines := bashSmellLines(root, []string{"slow_test.go", "gone_test.go"})

	if len(lines) != 1 || !strings.HasPrefix(lines[0], "gate: slow_test.go: ") || !strings.Contains(lines[0], "an Edit would have been denied for this; the commit gate refuses it") {
		t.Fatalf("lines = %q, want one denial line for slow_test.go", lines)
	}
}

// An advisory verdict is reported as it is to an Edit, with no claim of a
// refusal.
func TestBashSmellLines_AnAdvisoryVerdictIsNoDenial(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	mustWrite(t, filepath.Join(root, "widget.go"), "package m\n\n//nolint:errcheck\nfunc Size() int { return 1 }\n")

	lines := bashSmellLines(root, []string{"widget.go"})

	if len(lines) != 1 || !strings.HasPrefix(lines[0], "gate: widget.go: ") || strings.Contains(lines[0], "denied") {
		t.Fatalf("lines = %q, want one advisory line", lines)
	}
}

func TestBashSmellLines_ACleanFileHasNoLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	mustWrite(t, filepath.Join(root, "widget.go"), "package m\n\nfunc Size() int { return 2 }\n")

	if lines := bashSmellLines(root, []string{"widget.go"}); len(lines) != 0 {
		t.Fatalf("lines = %q, want none", lines)
	}
}

// What follows the runs: the notes ride the first line, a smell line is a
// line of its own even when nothing else spoke, and the detached runs start
// after, one lint per Go file and a mutation run for the files of a green
// root that are still there.
func TestBashGateFinish_TheNotesRideTheFirstLineAndASmellIsALineOfItsOwn(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	root := lawRepo(t)
	mustWrite(t, filepath.Join(root, "slow_test.go"), parityWidgetTest)

	text, _ := bashGateFinish("s", root, []string{"slow_test.go"}, "gofmt formatted x.go", nil, nil)

	lines := strings.Split(text, "\n")
	if len(lines) != 2 || lines[0] != "gate: gofmt formatted x.go" || !strings.HasPrefix(lines[1], "gate: slow_test.go: ") {
		t.Fatalf("text = %q, want the gofmt note as the gate line and the smell beneath", text)
	}

	text, _ = bashGateFinish("s", root, []string{"widget.go"}, "", []string{"gate: go test ./... → green"}, nil)
	if text != "gate: go test ./... → green" {
		t.Fatalf("text = %q, want the run's line alone when nothing more was found", text)
	}
}

// ratchet: test_removed TestBashGateFinish_StartsLintForEveryGoFileAndMutationsOnlyForLiveGreenFiles: a shell write starts no lint or mutation run now; TestBashGateFinish_StartsNoLintOrMutationRun holds that
