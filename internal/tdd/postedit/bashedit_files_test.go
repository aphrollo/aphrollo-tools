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

func TestLintPackages_GroupsGoFilesByDirectoryInFirstSeenOrder(t *testing.T) {
	root := lawRepo(t)
	p := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

	got := lintPackages([]string{p("b/b1.go"), p("README.md"), p("a/a1.go"), p("b/b2.go"), p("top.go")})

	want := []lintPackage{
		{root: root, dir: "b", rels: []string{"b/b1.go", "b/b2.go"}},
		{root: root, dir: "a", rels: []string{"a/a1.go"}},
		{root: root, dir: ".", rels: []string{"top.go"}},
	}
	if len(got) != len(want) {
		t.Fatalf("packages = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].root != want[i].root || got[i].dir != want[i].dir || !slices.Equal(got[i].rels, want[i].rels) {
			t.Errorf("package %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Each package is one run and one patch, and a finding in a file of another
// package is not this one's.
func TestLintEditedFiles_RunsOncePerPackageAndNamesEachFilesFindings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	prevLook, prevLoad, prevRun := lintEditLook, lintEditLoad, lintEditRun
	t.Cleanup(func() { lintEditLook, lintEditLoad, lintEditRun = prevLook, prevLoad, prevRun })
	lintEditLook = func() bool { return true }
	lintEditLoad = func() (float64, int, bool) { return 0, 8, true }
	var runs [][]string
	lintEditRun = func(_ string, args []string) (string, bool) {
		runs = append(runs, args)
		if args[len(args)-1] == "./a" {
			return "a/a1.go:1:1: one (x)\nb/b1.go:1:1: wrong package (y)\n", false
		}
		return "b/b1.go:2:1: two (z)\n", false
	}
	mustWrite(t, filepath.Join(root, "a", "a1.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "a", "a2.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "b", "b1.go"), "package b\n")
	var known []string

	note := lintEditedFiles([]string{filepath.Join(root, "a", "a1.go"), filepath.Join(root, "a", "a2.go"), filepath.Join(root, "b", "b1.go")}, &known)

	if len(runs) != 2 {
		t.Fatalf("runs = %v, want one per package", runs)
	}
	want := []string{"a/a1.go:1:1: one (x)", "b/b1.go:2:1: two (z)"}
	if !slices.Equal(known, want) {
		t.Errorf("findings = %v, want %v", known, want)
	}
	if note != "golangci-lint: "+strings.Join(want, "; ") {
		t.Errorf("note = %q", note)
	}
}

// A run past its budget ends the edit's lint: its note stands alone and later
// packages are not run.
func TestLintEditedFiles_ATimeoutIsTheNoteAndStopsTheRest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	prevLook, prevLoad, prevRun := lintEditLook, lintEditLoad, lintEditRun
	t.Cleanup(func() { lintEditLook, lintEditLoad, lintEditRun = prevLook, prevLoad, prevRun })
	lintEditLook = func() bool { return true }
	lintEditLoad = func() (float64, int, bool) { return 0, 8, true }
	runs := 0
	lintEditRun = func(string, []string) (string, bool) {
		runs++
		return "", true
	}
	mustWrite(t, filepath.Join(root, "a", "a1.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "b", "b1.go"), "package b\n")

	note := lintEditedFiles([]string{filepath.Join(root, "a", "a1.go"), filepath.Join(root, "b", "b1.go")}, nil)

	if !strings.HasPrefix(note, "golangci-lint did not finish in ") || runs != 1 {
		t.Fatalf("note = %q after %d run(s), want the timeout note after one", note, runs)
	}
}

// A file the command left as HEAD has it has no changed lines to lint, and a
// package of such files is not run.
func TestLintEditedFiles_AFileWithNoChangedLinesIsNotLinted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	prevLook, prevLoad, prevRun := lintEditLook, lintEditLoad, lintEditRun
	t.Cleanup(func() { lintEditLook, lintEditLoad, lintEditRun = prevLook, prevLoad, prevRun })
	lintEditLook = func() bool { return true }
	lintEditLoad = func() (float64, int, bool) { return 0, 8, true }
	lintEditRun = func(string, []string) (string, bool) {
		t.Fatal("the linter ran over a file with no changed lines")
		return "", false
	}

	if note := lintEditedFiles([]string{filepath.Join(root, "widget.go")}, nil); note != "" {
		t.Fatalf("note = %q, want none", note)
	}
}

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
	live := []string{filepath.Join(root, "slow_test.go")}

	text, _ := bashGateFinish("s", root, []string{"slow_test.go"}, live, "gofmt formatted x.go", nil, nil)

	lines := strings.Split(text, "\n")
	if len(lines) != 2 || lines[0] != "gate: gofmt formatted x.go" || !strings.HasPrefix(lines[1], "gate: slow_test.go: ") {
		t.Fatalf("text = %q, want the gofmt note as the gate line and the smell beneath", text)
	}

	text, _ = bashGateFinish("s", root, []string{"widget.go"}, nil, "", []string{"gate: go test ./... → green"}, nil)
	if text != "gate: go test ./... → green" {
		t.Fatalf("text = %q, want the run's line alone when nothing more was found", text)
	}
}

// ratchet: test_removed TestBashGateFinish_StartsLintForEveryGoFileAndMutationsOnlyForLiveGreenFiles: a shell write starts no lint or mutation run now; TestBashGateFinish_StartsNoLintOrMutationRun holds that
