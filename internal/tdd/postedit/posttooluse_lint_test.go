package postedit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/lock"
)

// lintSeams installs the edit-time lint's seams for one test: the linter
// present, the box idle, and a run that records its argv and answers out. It
// returns the recorded runs.
func lintSeams(t *testing.T, out string, timedOut bool) *[][]string {
	t.Helper()
	var runs [][]string
	prevLook, prevLoad, prevRun := lintEditLook, lintEditLoad, lintEditRun
	t.Cleanup(func() { lintEditLook, lintEditLoad, lintEditRun = prevLook, prevLoad, prevRun })
	lintEditLook = func() bool { return true }
	lintEditLoad = func() (float64, int, bool) { return 1, 8, true }
	lintEditRun = func(root string, args []string) (string, bool) {
		runs = append(runs, args)
		return out, timedOut
	}
	return &runs
}

// editedWidget is lawRepo with widget.go rewritten: an uncommitted edit.
func editedWidget(t *testing.T) (root, src string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root = lawRepo(t)
	src = filepath.Join(root, "widget.go")
	mustWrite(t, src, "package m\n\nfunc Size() int { return 2 }\n")
	return root, src
}

// TestLintEdited_PutsTheEditedFilesFindingsOnTheGateLine is issue #1006: a
// finding the commit gate's lint would refuse over is knowable at the edit.
// The linter runs its fast set over the edited file's package, limited to the
// lines the edit changed, and the findings in the edited file ride on the
// gate line; another file's do not.
func TestLintEdited_PutsTheEditedFilesFindingsOnTheGateLine(t *testing.T) {
	_, src := editedWidget(t)
	out := "widget.go:3:22: ineffectual assignment to x (ineffassign)\n" +
		"other.go:9:1: unused thing (ineffassign)\n"
	runs := lintSeams(t, out, false)

	got := lintEdited(src)

	if want := "golangci-lint: widget.go:3:22: ineffectual assignment to x (ineffassign)"; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if len(*runs) != 1 {
		t.Fatalf("%d lint runs, want 1", len(*runs))
	}
	args := (*runs)[0]
	joined := strings.Join(args, " ")
	for _, want := range []string{"run", "--fast-only", "--new-from-patch="} {
		if !strings.Contains(joined, want) {
			t.Errorf("lint argv %q lacks %q", joined, want)
		}
	}
	if pkg := args[len(args)-1]; pkg != "." {
		t.Errorf("a file at the repo top lints package %q, want \".\"", pkg)
	}
}

// TestLintEdited_LintsTheEditedFilesOwnPackage: a file below the top lints
// its directory alone, not the module.
func TestLintEdited_LintsTheEditedFilesOwnPackage(t *testing.T) {
	root, _ := editedWidget(t)
	mustWrite(t, filepath.Join(root, "sub", "dir", "part.go"), "package dir\n\nfunc Part() int { return 1 }\n")
	runs := lintSeams(t, "", false)

	lintEdited(filepath.Join(root, "sub", "dir", "part.go"))

	if len(*runs) != 1 || (*runs)[0][len((*runs)[0])-1] != "./sub/dir" {
		t.Fatalf("runs = %v, want one run over ./sub/dir", *runs)
	}
}

// TestLintEdited_ANonGoFileAndACleanFileRunNothing pins the two edits with
// nothing to lint: a file that is not Go, and a Go file the edit left as it
// is at HEAD.
func TestLintEdited_ANonGoFileAndACleanFileRunNothing(t *testing.T) {
	root, _ := editedWidget(t)
	runs := lintSeams(t, "widget.go:1:1: x (y)\n", false)
	mustWrite(t, filepath.Join(root, "notes.md"), "words\n")
	if got := lintEdited(filepath.Join(root, "notes.md")); got != "" {
		t.Errorf("a markdown file was linted: %q", got)
	}
	mustWrite(t, filepath.Join(root, "widget.go"), "package m\n\nfunc Size() int { return 1 }\n")
	if got := lintEdited(filepath.Join(root, "widget.go")); got != "" {
		t.Errorf("a file identical to HEAD was linted: %q", got)
	}
	if len(*runs) != 0 {
		t.Fatalf("%d lint runs for edits with nothing to lint, want 0", len(*runs))
	}
}

// TestLintEdited_AnAbsentLinterIsSkipped: no golangci-lint on the box is no
// lint, silently, as the commit gate treats it.
func TestLintEdited_AnAbsentLinterIsSkipped(t *testing.T) {
	_, src := editedWidget(t)
	runs := lintSeams(t, "widget.go:3:1: x (y)\n", false)
	lintEditLook = func() bool { return false }
	if got := lintEdited(src); got != "" || len(*runs) != 0 {
		t.Fatalf("absent linter: note %q, %d runs; want neither", got, len(*runs))
	}
}

// TestLintEdited_ALoadedBoxIsSkipped: at twice the cores in runnable load the
// edit hook does not add a lint; one under it does.
func TestLintEdited_ALoadedBoxIsSkipped(t *testing.T) {
	_, src := editedWidget(t)
	runs := lintSeams(t, "widget.go:3:1: x (y)\n", false)
	lintEditLoad = func() (float64, int, bool) { return 15.99, 8, true }
	if got := lintEdited(src); got == "" || len(*runs) != 1 {
		t.Fatalf("load just under 2 per core: note %q, %d runs; want the lint to run", got, len(*runs))
	}
	lintEditLoad = func() (float64, int, bool) { return 16, 8, true }
	if got := lintEdited(src); got != "" || len(*runs) != 1 {
		t.Fatalf("load of exactly 2 per core: note %q, %d runs; want it skipped", got, len(*runs))
	}
	lintEditLoad = func() (float64, int, bool) { return 0, 0, false }
	if got := lintEdited(src); got == "" {
		t.Fatalf("an unreadable load must not stand the lint down")
	}
}

// TestLintEdited_AHeldLintLockIsSkipped: another lint holding the box-wide
// lock is a busy box, and the edit hook never waits for it.
func TestLintEdited_AHeldLintLockIsSkipped(t *testing.T) {
	root, src := editedWidget(t)
	runs := lintSeams(t, "widget.go:3:1: x (y)\n", false)
	release, ok := lock.TryAcquireLintLock("held", root)
	if !ok {
		t.Fatal("setup: the lint lock was already held")
	}
	defer release()
	if got := lintEdited(src); got != "" || len(*runs) != 0 {
		t.Fatalf("held lock: note %q, %d runs; want neither", got, len(*runs))
	}
}

// TestLintEdited_ARunPastItsBudgetIsNamedAndBacksOff: a lint that does not
// finish in its budget is named on the gate line, and the next edits skip it
// for the backoff window instead of paying the timeout again.
func TestLintEdited_ARunPastItsBudgetIsNamedAndBacksOff(t *testing.T) {
	_, src := editedWidget(t)
	runs := lintSeams(t, "", true)

	got := lintEdited(src)

	if want := "golangci-lint did not finish in " + lintEditBudget.String() + "; skipped for " + lintEditBackoff.String(); got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if again := lintEdited(src); again != "" || len(*runs) != 1 {
		t.Fatalf("second edit inside the backoff: note %q, %d runs; want no note and still 1 run", again, len(*runs))
	}
}

// TestLintEdited_ContentionWithAnOutsideLintSaysNothing: golangci-lint's own
// machine-wide lock held by a lint this gate did not start is contention, not
// a finding.
func TestLintEdited_ContentionWithAnOutsideLintSaysNothing(t *testing.T) {
	_, src := editedWidget(t)
	lintSeams(t, "Error: parallel golangci-lint is running\n", false)
	if got := lintEdited(src); got != "" {
		t.Fatalf("contention became a note: %q", got)
	}
}

// TestLintEdited_NamesFiveFindingsAndCountsTheRest pins the cap.
func TestLintEdited_NamesFiveFindingsAndCountsTheRest(t *testing.T) {
	_, src := editedWidget(t)
	var out strings.Builder
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&out, "widget.go:%d:1: finding %d (x)\n", i, i)
	}
	lintSeams(t, out.String(), false)
	got := lintEdited(src)
	if !strings.Contains(got, "finding 5 (x)") || strings.Contains(got, "finding 6 (x)") || !strings.HasSuffix(got, " and 1 more") {
		t.Fatalf("note = %q, want five findings and \"and 1 more\"", got)
	}
	lintSeams(t, strings.Join(strings.Split(out.String(), "\n")[:5], "\n")+"\n", false)
	if got := lintEdited(src); strings.Contains(got, "more") || !strings.Contains(got, "finding 5 (x)") {
		t.Fatalf("five findings: note = %q, want all five and no count", got)
	}
}

// TestPostEdit_PutsLintFindingsOnTheGateLine wires the note into the hook: a
// finding rides on the edit's gate line beside the run verdict.
func TestPostEdit_PutsLintFindingsOnTheGateLine(t *testing.T) {
	_, src := editedWidget(t)
	lintSeams(t, "widget.go:3:22: ineffectual assignment to x (ineffassign)\n", false)

	got := PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	line := firstLine(got)
	if !strings.Contains(line, "→ green") || !strings.Contains(line, "golangci-lint: widget.go:3:22: ineffectual assignment to x (ineffassign)") {
		t.Fatalf("gate line %q lacks the verdict or the lint finding", line)
	}
}

// TestLintEdited_RealGolangciLintFlagsOnlyTheTouchedLines runs the real
// linter over a module with one old finding and one the edit adds: only the
// added line is reported. Skipped where golangci-lint is not installed.
func TestLintEdited_RealGolangciLintFlagsOnlyTheTouchedLines(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed") // skip-ok: the e2e half of the lint seam tests needs the real binary
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	old := "package m\n\nfunc Old() int {\n\tx := 1\n\tx = 2\n\tx = 3\n\treturn x\n}\n"
	mustWrite(t, filepath.Join(root, "widget.go"), old)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "init")
	mustWrite(t, filepath.Join(root, "widget.go"), old+"\nfunc New() int {\n\ty := 1\n\ty = 2\n\ty = 3\n\treturn y\n}\n")
	prevLook, prevLoad, prevRun := lintEditLook, lintEditLoad, lintEditRun
	t.Cleanup(func() { lintEditLook, lintEditLoad, lintEditRun = prevLook, prevLoad, prevRun })
	lintEditLook = func() bool { return true }
	lintEditLoad = func() (float64, int, bool) { return 0, 0, false }
	lintEditRun = runLintEdit

	started := time.Now()
	got := lintEdited(filepath.Join(root, "widget.go"))
	t.Logf("edit-time lint took %s: %s", time.Since(started).Round(time.Millisecond), got)

	if !strings.Contains(got, "widget.go:11:") || strings.Contains(got, "widget.go:5:") || strings.Contains(got, "widget.go:4:") {
		t.Fatalf("note = %q, want the finding on the added lines (11) and none on the old ones (4, 5)", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(os.TempDir(), "aphrollo-lint-*.patch")); len(leftovers) != 0 {
		t.Errorf("the patch file was left behind: %v", leftovers)
	}
}
