package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A script that rewrites many files in one Bash call gets, for each file it
// changed, what an Edit of that file gets: gofmt, the deny laws judged on the
// formatted bytes, the smell checks, an edit-ledger record with the run's
// verdict, the linter and the mutation run, and its files' packages in the
// suite, which runs once per root. The one difference is when a refusal can
// come: an Edit is denied before it lands, a Bash write is named right after.

// parityWidget needs gofmt and calls the function lawRepo's deny law forbids.
const parityWidget = "package m\n\nfunc  Size()  int {return forbidden()}\n"

const parityWidgetFormatted = "package m\n\nfunc Size() int { return forbidden() }\n"

// parityWidgetTest is gofmt-clean and sleeps, which a test file may not.
const parityWidgetTest = "package m\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSize(t *testing.T) {\n\ttime.Sleep(time.Second)\n\tif Size() != 1 {\n\t\tt.Fatal(\"size\")\n\t}\n}\n"

// noInlineLint turns the edit-time linter off for a test that is not about it.
func noInlineLint(t *testing.T) {
	t.Helper()
	prev := lintEditLook
	lintEditLook = func() bool { return false }
	t.Cleanup(func() { lintEditLook = prev })
}

// collectRuns is a suite runner that passes and records every command it is
// asked to run.
func collectRuns(runs *[]Runner) SuiteRunner {
	return func(r Runner, _ string) SuiteResult {
		*runs = append(*runs, r)
		return SuiteResult{Passed: true, Output: "ok  \tm\t0.003s\n"}
	}
}

// writePayload is the PreToolUse payload of a Write of content to path.
func writePayload(t *testing.T, path, content string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"tool_name":  "Write",
		"tool_input": map[string]string{"file_path": path, "content": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ledgerRow is one edit of the ledger without the parts that differ by run:
// its id, time and absolute path.
type ledgerRow struct{ file, class, outcome string }

func ledgerRows(t *testing.T, root string) []ledgerRow {
	t.Helper()
	var rows []ledgerRow
	for _, e := range loadEditLedger(root) {
		rel, err := filepath.Rel(root, e.File)
		if err != nil {
			t.Fatal(err)
		}
		row := ledgerRow{file: filepath.ToSlash(rel), class: e.Class, outcome: "none"}
		if e.Verdict != nil {
			row.outcome = e.Verdict.Outcome
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b ledgerRow) int { return strings.Compare(a.file, b.file) })
	return rows
}

func TestBashEdit_GetsWhatAnEditGets(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	files := []struct{ rel, content string }{{"widget.go", parityWidget}, {"widget_test.go", parityWidgetTest}}

	// The Edit path: one Write per file, each judged before and after.
	editRoot := lawRepo(t)
	var editRuns []Runner
	var editLines []string
	var editSmell Decision
	for _, f := range files {
		path := filepath.Join(editRoot, f.rel)
		d, err := DecidePreEdit(writePayload(t, path, f.content))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(f.rel, "_test.go") {
			editSmell = d
		}
		mustWrite(t, path, f.content)
		editLines = append(editLines, PostEdit(postPayload("Write", path), collectRuns(&editRuns)))
	}

	// The Bash path: one script writes both.
	bashRoot := lawRepo(t)
	cmd := "./regen-all.sh"
	PreBash(bashPayload(t, "s1070", bashRoot, cmd))
	for _, f := range files {
		mustWrite(t, filepath.Join(bashRoot, f.rel), f.content)
	}
	var bashRuns []Runner
	bashText := PostBash(bashPayload(t, "s1070", bashRoot, cmd), collectRuns(&bashRuns))

	// Formatting: the same bytes.
	for _, root := range []string{editRoot, bashRoot} {
		got, err := os.ReadFile(filepath.Join(root, "widget.go"))
		if err != nil || string(got) != parityWidgetFormatted {
			t.Errorf("%s/widget.go = %q (%v), want gofmt's %q", root, got, err, parityWidgetFormatted)
		}
	}
	if !strings.Contains(editLines[0], "gofmt formatted "+filepath.Join(editRoot, "widget.go")) {
		t.Errorf("the Edit's gate line does not name the gofmt: %q", editLines[0])
	}
	if strings.Count(bashText, "gofmt formatted") != 1 || !strings.Contains(bashText, "gofmt formatted "+filepath.Join(bashRoot, "widget.go")) {
		t.Errorf("the Bash call's gate line must name the one file gofmt changed, got %q", bashText)
	}

	// Law findings: the same finding, on the gate line.
	for name, line := range map[string]string{"edit": firstLine(editLines[0]), "bash": firstLine(bashText)} {
		if !strings.Contains(line, "ratchet would refuse the commit: no-forbidden: widget.go:3") {
			t.Errorf("%s: the gate line does not name the file and law: %q", name, line)
		}
	}

	// Smells: what the Edit was denied for, the Bash call is told.
	if editSmell.Action != Block || editSmell.Reason == "" {
		t.Fatalf("the Edit of the sleeping test was not denied: %+v", editSmell)
	}
	if !strings.Contains(bashText, editSmell.Reason) || !strings.Contains(bashText, "widget_test.go") {
		t.Errorf("the Bash call must be told what an Edit was denied for (%q), got %q", editSmell.Reason, bashText)
	}

	// Ledger: one edit per file, each with the verdict of the run that judged it.
	wantRows := []ledgerRow{{"widget.go", "production", "green"}, {"widget_test.go", "test-only", "green"}}
	if got := ledgerRows(t, editRoot); !slices.Equal(got, wantRows) {
		t.Errorf("the Edit path's ledger = %+v, want %+v", got, wantRows)
	}
	if got := ledgerRows(t, bashRoot); !slices.Equal(got, wantRows) {
		t.Errorf("the Bash path's ledger = %+v, want %+v", got, wantRows)
	}

	// Gate lines: a green for the change, with one suite run for the call.
	if !strings.Contains(firstLine(bashText), "→ green") {
		t.Errorf("the Bash call's gate line is not a green: %q", bashText)
	}
	if len(editRuns) != 2 || len(bashRuns) != 1 {
		t.Errorf("suite runs: %d for two Edits, %d for one Bash call; want 2 and 1", len(editRuns), len(bashRuns))
	}
}

// A law is judged on the bytes the commit will carry: a hit that gofmt removes
// is no refusal.
func TestPostBash_JudgesTheLawsOnTheFormattedBytes(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	root := lawRepo(t)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-spacey.toml"), `
name = "no-spacey"
description = "no double space after func"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "func  Size"
`)
	cmd := "./regen.sh"
	PreBash(bashPayload(t, "s1070law", root, cmd))
	mustWrite(t, filepath.Join(root, "widget.go"), "package m\n\nfunc  Size()  int {return 1}\n")

	got := PostBash(bashPayload(t, "s1070law", root, cmd), fakeRun(true, "ok\nPASS"))

	if strings.Contains(got, "ratchet") {
		t.Fatalf("a hit gofmt removed must not be reported, got %q", got)
	}
	if !strings.Contains(got, "gofmt formatted") {
		t.Fatalf("the file was not formatted: %q", got)
	}
}

// Every file the command changed is an edit of the ledger and shares the
// verdict of the run, and a root the call's one deferral left unexercised
// still has its files recorded.
func TestPostBash_RecordsEveryChangedFileInTheLedger(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	root := lawRepo(t)
	cmd := "./regen.sh"
	PreBash(bashPayload(t, "s1070ledger", root, cmd))
	mustWrite(t, filepath.Join(root, "a.go"), "package m\n\nfunc A() int { return 1 }\n")
	mustWrite(t, filepath.Join(root, "b.go"), "package m\n\nfunc B() int { return 2 }\n")

	var runs []Runner
	PostBash(bashPayload(t, "s1070ledger", root, cmd), collectRuns(&runs))

	want := []ledgerRow{{"a.go", "production", "green"}, {"b.go", "production", "green"}}
	if got := ledgerRows(t, root); !slices.Equal(got, want) {
		t.Fatalf("ledger = %+v, want %+v", got, want)
	}
	if len(runs) != 1 {
		t.Fatalf("the suite ran %d times for one root, want 1", len(runs))
	}
}

func TestPostBash_RecordsTheFilesOfARootItDidNotRun(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	noInlineLint(t)
	gitRoot := t.TempDir()
	write(t, gitRoot, "svcA/go.mod", "module example.com/svcA\n\ngo 1.26\n")
	write(t, gitRoot, "svcA/main.go", "package main\n\nfunc main() {}\n")
	write(t, gitRoot, "svcB/go.mod", "module example.com/svcB\n\ngo 1.26\n")
	write(t, gitRoot, "svcB/main.go", "package main\n\nfunc main() {}\n")
	gitInit(t, gitRoot)
	gitDo(t, gitRoot, "add", "-A")
	gitDo(t, gitRoot, "commit", "-qm", "base")
	cmd := "./regen.sh"
	PreBash(bashPayload(t, "s1070roots", gitRoot, cmd))
	write(t, gitRoot, "svcA/main.go", "package main\n\nfunc main() { println(1) }\n")
	write(t, gitRoot, "svcB/main.go", "package main\n\nfunc main() { println(2) }\n")
	fakePhases(t) // svcA's build never finishes within the budget: it defers

	got := PostBash(bashPayload(t, "s1070roots", gitRoot, cmd), fakeRun(true, "ok"))

	if !strings.Contains(got, "skipped") {
		t.Fatalf("the deferral must name the root it left unexercised: %q", got)
	}
	rows := ledgerRows(t, filepath.Join(gitRoot, "svcB"))
	if len(rows) != 1 || rows[0].file != "main.go" || rows[0].outcome != "none" {
		t.Fatalf("the unexercised root's file must be recorded with no verdict, got %+v", rows)
	}
	if rows := ledgerRows(t, filepath.Join(gitRoot, "svcA")); len(rows) != 1 {
		t.Fatalf("the exercised root's file must be recorded once, got %+v", rows)
	}
}

// A file the command deleted is no edit of any ledger and nothing to format.
func TestPostBash_ADeletedFileIsNoEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	root := lawRepo(t)
	cmd := "rm widget.go"
	PreBash(bashPayload(t, "s1070rm", root, cmd))
	if err := os.Remove(filepath.Join(root, "widget.go")); err != nil {
		t.Fatal(err)
	}

	var runs []Runner
	got := PostBash(bashPayload(t, "s1070rm", root, cmd), collectRuns(&runs))

	if rows := ledgerRows(t, root); len(rows) != 0 {
		t.Fatalf("a deletion must record no edit, got %+v", rows)
	}
	if strings.Contains(got, "gofmt") {
		t.Fatalf("a deletion has nothing to format: %q", got)
	}
}

// Smells are judged over the lines the command added against HEAD, as an
// Edit's are over the lines it adds.
func TestPostBash_SmellsAreJudgedOverTheLinesTheCommandAdded(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	root := lawRepo(t)
	mustWrite(t, filepath.Join(root, "old_test.go"), parityWidgetTest)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "a test that already sleeps")
	cmd := "./append.sh"

	PreBash(bashPayload(t, "s1070smell", root, cmd))
	mustWrite(t, filepath.Join(root, "old_test.go"), parityWidgetTest+"\n// a note\n")
	got := PostBash(bashPayload(t, "s1070smell", root, cmd), fakeRun(true, "ok\nPASS"))
	if strings.Contains(got, "sleep") {
		t.Fatalf("a smell already at HEAD is not the command's, got %q", got)
	}

	PreBash(bashPayload(t, "s1070smell2", root, cmd))
	mustWrite(t, filepath.Join(root, "old_test.go"), parityWidgetTest+"\nfunc TestSlow(t *testing.T) { time.Sleep(time.Second) }\n")
	got = PostBash(bashPayload(t, "s1070smell2", root, cmd), fakeRun(true, "ok\nPASS"))
	if !strings.Contains(got, "old_test.go") || !strings.Contains(strings.ToLower(got), "sleep") {
		t.Fatalf("a sleep the command added must be named with its file, got %q", got)
	}
}

// The run of a root covers the package of every file the command changed in
// it, as a run per Edit would have.
func TestPostBash_RunsEveryPackageTheCommandChanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	root := lawRepo(t)
	cmd := "./regen.sh"
	PreBash(bashPayload(t, "s1070pkgs", root, cmd))
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n\nfunc A() int { return 1 }\n")
	mustWrite(t, filepath.Join(root, "b", "b.go"), "package b\n\nfunc B() int { return 2 }\n")

	var runs []Runner
	PostBash(bashPayload(t, "s1070pkgs", root, cmd), collectRuns(&runs))

	if len(runs) != 1 {
		t.Fatalf("the suite ran %d times, want 1: %+v", len(runs), runs)
	}
	args := strings.Join(runs[0].Args, " ")
	if !strings.Contains(args, "./a") || !strings.Contains(args, "./b") {
		t.Fatalf("the run %q must cover both changed packages", runs[0].Cmd+" "+args)
	}
}

// ratchet: test_removed TestPostBash_LintsTheChangedGoFilesOncePerPackage: the edit-time lint moved into the run (lintrun.go), so a Bash write no longer lints inline

// ratchet: test_removed TestPostBash_StartsTheMutationRunAfterAGreenCall: inverted into TestPostBash_StartsNoMutationRunAfterAGreenCall, a Bash call starts no mutation run now
//
// A green Bash call over a changed Go file starts no mutation run: the commit
// gate and CI hold mutation.
func TestPostBash_StartsNoMutationRunAfterAGreenCall(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	noInlineLint(t)
	jobs := recordEditRunSpawns(t, nil)
	cmd := "./regen.sh"
	PreBash(bashPayload(t, "s1070mut", root, cmd))
	mustWrite(t, src, "package m\n\nfunc Widget(n int) bool { return n > 2 }\n")

	got := PostBash(bashPayload(t, "s1070mut", root, cmd), greenRun)

	if !strings.Contains(got, "green") {
		t.Fatalf("the call is not green: %q", got)
	}
	if len(*jobs) != 0 {
		t.Fatalf("mutation jobs = %+v, want none", *jobs)
	}
}

func TestPostBash_StartsNoMutationRunAfterARedCall(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	noInlineLint(t)
	jobs := recordEditRunSpawns(t, nil)
	cmd := "./regen.sh"
	PreBash(bashPayload(t, "s1070mutred", root, cmd))
	mustWrite(t, src, "package m\n\nfunc Widget(n int) bool { return n > 2 }\n")

	PostBash(bashPayload(t, "s1070mutred", root, cmd), redRun)

	if len(*jobs) != 0 {
		t.Fatalf("a red call started mutation jobs: %+v", *jobs)
	}
}
