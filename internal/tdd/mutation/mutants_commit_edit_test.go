package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The edit-time form runs the commit stage over the lines an edit changed
// against HEAD, in the working tree as it stands, and records how it ended in
// a file the next hook reads.

// editRepo is a committed Go repository that declares mutants-at-commit, and
// gate.go edited in the working tree: two mutants on line 4, unstaged.
func editRepo(t *testing.T) string {
	t.Helper()
	_, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = \"block\"\n")
	gitDo(t, root, "reset", "-q", "gate/gate.go")
	return root
}

func readDone(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the run left no result at %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

func TestRunMutantsEdit_ASurvivorOfTheEditedLinesIsRefusedAndRecorded(t *testing.T) {
	root := editRepo(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	done := filepath.Join(t.TempDir(), "done")

	var code int
	stderr := captureStderr(t, func() { code = RunMutantsEdit(root, "gate/gate.go", done, nil) })

	if code != 1 || readDone(t, done) != "refused" {
		t.Errorf("exit %d and result %q, want 1 and refused", code, readDone(t, done))
	}
	if !strings.Contains(stderr, "gate/gate.go:4:7: CONDITIONALS_BOUNDARY") {
		t.Errorf("stderr = %q, want the survivor named", stderr)
	}
	if s.count() != 2 {
		t.Errorf("go test ran %d times, want 2 (one whole-package run per mutant)", s.count())
	}
}

func TestRunMutantsEdit_CaughtMutantsAreRecordedOK(t *testing.T) {
	root := editRepo(t)
	scriptGo(t, killsUnderTheMutant)
	done := filepath.Join(t.TempDir(), "done")
	var code int
	stderr := captureStderr(t, func() { code = RunMutantsEdit(root, "gate/gate.go", done, nil) })
	if code != 0 || readDone(t, done) != "ok" {
		t.Errorf("exit %d and result %q, want 0 and ok", code, readDone(t, done))
	}
	if !strings.Contains(stderr, "2 tested, 2 caught") {
		t.Errorf("stderr = %q, want the counts", stderr)
	}
}

// The edit's lines are the ones that differ from HEAD: a file the edit left as
// HEAD has, and lines an earlier commit already holds, are not mutated.
func TestRunMutantsEdit_OnlyWhatDiffersFromHEADIsMeasured(t *testing.T) {
	root := editRepo(t)
	write(t, root, "gate/gate.go", commitBaseSource)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
	done := filepath.Join(t.TempDir(), "done")

	code := RunMutantsEdit(root, "gate/gate.go", done, nil)

	if code != 0 || s.count() != 0 || readDone(t, done) != "ok" {
		t.Errorf("exit %d after %d runs with result %q, want a clean pass that ran nothing", code, s.count(), readDone(t, done))
	}
}

// A file no commit holds yet is entirely the edit's own.
func TestRunMutantsEdit_ANewFileIsMeasuredWhole(t *testing.T) {
	root := editRepo(t)
	write(t, root, "gate/fresh.go", "package gate\n\nfunc Big(n int) bool {\n\treturn n > 10\n}\n")
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	done := filepath.Join(t.TempDir(), "done")

	var code int
	stderr := captureStderr(t, func() { code = RunMutantsEdit(root, "gate/fresh.go", done, nil) })

	if code != 1 || !strings.Contains(stderr, "gate/fresh.go:4:11: CONDITIONALS_BOUNDARY") {
		t.Errorf("exit %d stderr %q, want the new file's survivor named", code, stderr)
	}
}

func TestRunMutantsEdit_UndeclaredRunsNothingAndSaysOK(t *testing.T) {
	root := editRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nundercover = true\n")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
	done := filepath.Join(t.TempDir(), "done")
	if code := RunMutantsEdit(root, "gate/gate.go", done, nil); code != 0 || s.count() != 0 || readDone(t, done) != "ok" {
		t.Errorf("exit %d after %d runs with result %q, want a pass that ran nothing", code, s.count(), readDone(t, done))
	}
}

func TestEditRelPath_InsideTheRepoOnly(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repo")
	for _, tc := range []struct {
		name string
		file string
		want string
		ok   bool
	}{
		{"a relative file", "gate/gate.go", "gate/gate.go", true},
		{"an absolute file inside", filepath.Join(root, "gate", "gate.go"), "gate/gate.go", true},
		{"a file at the root", "x.go", "x.go", true},
		{"a name that starts with two dots", "..x.go", "..x.go", true},
		{"a file above the root", filepath.Join("..", "x.go"), "", false},
		{"an absolute file beside the root", filepath.Join(filepath.Dir(root), "elsewhere.go"), "", false},
	} {
		got, ok := editRelPath(root, tc.file)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: editRelPath = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// A path outside the repository is not measured, and is not a crash.
func TestRunMutantsEdit_APathOutsideTheRepoIsRecordedOK(t *testing.T) {
	root := editRepo(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
	done := filepath.Join(t.TempDir(), "done")
	if code := RunMutantsEdit(root, filepath.Join(t.TempDir(), "elsewhere.go"), done, nil); code != 0 || s.count() != 0 {
		t.Errorf("exit %d after %d runs, want a pass that ran nothing", code, s.count())
	}
	if got := readDone(t, done); got != "ok" {
		t.Errorf("result = %q, want ok", got)
	}
}
