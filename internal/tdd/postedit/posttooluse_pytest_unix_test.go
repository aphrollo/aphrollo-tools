//go:build !windows

package postedit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pytestEditRoot is a pytest root with one test file, on a PATH that holds no
// interpreter, so only what the root itself carries can run its tests.
func pytestEditRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	write(t, root, "pyproject.toml", "[tool.pytest.ini_options]\n")
	write(t, root, "tests/test_widget.py", "def test_widget():\n    assert True\n")
	return root
}

// venvPython writes root's .venv/bin/python as a script that exits with
// importExit whatever it is asked, which is all the pytest import probe reads.
func venvPython(t *testing.T, root, importExit string) string {
	t.Helper()
	write(t, root, ".venv/bin/python", "#!/bin/sh\nexit "+importExit+"\n")
	p := filepath.Join(root, ".venv", "bin", "python")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func recordPytestEditRuns(ran *[]Runner) SuiteRunner {
	return func(r Runner, _ string) SuiteResult {
		*ran = append(*ran, r)
		return SuiteResult{Passed: true, Output: "1 passed in 0.01s\n"}
	}
}

// The edit hook runs a pytest root's tests under the root's own virtualenv
// interpreter, as `python -m pytest`, never a bare `pytest` from PATH.
func TestPostEdit_RunsAPytestRootUnderItsOwnVenvInterpreter(t *testing.T) {
	root := pytestEditRoot(t)
	venv := venvPython(t, root, "0")

	var ran []Runner
	PostEdit(postPayload("Edit", filepath.Join(root, "tests", "test_widget.py")), recordPytestEditRuns(&ran))

	want := []string{"-m", "pytest", "-q", "tests/test_widget.py"}
	if len(ran) != 1 || ran[0].Cmd != venv || !slices.Equal(ran[0].Args, want) {
		t.Fatalf("edit hook ran %+v, want one %s %v", ran, venv, want)
	}
}

// A pytest root with no interpreter anywhere runs nothing, and the line is an
// inconclusive SKIPPED naming the missing interpreter, never a red.
func TestPostEdit_SkipsAPytestRootWithNoInterpreterAndSaysWhy(t *testing.T) {
	root := pytestEditRoot(t)

	var ran []Runner
	got := PostEdit(postPayload("Edit", filepath.Join(root, "tests", "test_widget.py")), recordPytestEditRuns(&ran))

	if len(ran) != 0 {
		t.Fatalf("edit hook ran %+v with no interpreter, want nothing run", ran)
	}
	if !strings.Contains(got, "SKIPPED") || !strings.Contains(got, "no python interpreter") || !strings.Contains(got, "NOT tested") {
		t.Fatalf("edit hook line = %q, want an inconclusive SKIPPED line naming the missing interpreter", got)
	}
	if strings.Contains(got, "red") {
		t.Fatalf("edit hook line = %q, must never read red", got)
	}
}

// A virtualenv whose interpreter cannot import pytest is a missing tool too:
// nothing runs, and the line names that interpreter.
func TestPostEdit_SkipsAPytestRootWhoseVenvLacksPytestAndSaysWhy(t *testing.T) {
	root := pytestEditRoot(t)
	venv := venvPython(t, root, "1")

	var ran []Runner
	got := PostEdit(postPayload("Edit", filepath.Join(root, "tests", "test_widget.py")), recordPytestEditRuns(&ran))

	if len(ran) != 0 {
		t.Fatalf("edit hook ran %+v, want nothing run", ran)
	}
	if !strings.Contains(got, "SKIPPED") || !strings.Contains(got, "pytest is not importable by "+venv) {
		t.Fatalf("edit hook line = %q, want a SKIPPED line naming %s", got, venv)
	}
}
