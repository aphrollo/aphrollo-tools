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

// A pytest run that never got past collection because its interpreter lacks a
// third-party module (issue #1223) is not a red: the line says NOT TESTED,
// names the module and the fix, and the gate log records the verdict.
func TestPostEdit_AMissingThirdPartyModuleIsNotTestedNeverRed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := pytestEditRoot(t)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	venvPython(t, root, "0")
	out := "ERROR collecting tests/test_widget.py\nE   ModuleNotFoundError: No module named 'pyseto'\n" +
		"!!!!!!!! Interrupted: 1 error during collection !!!!!!!!\n1 error in 0.10s\n"

	got := PostEdit(postPayload("Edit", filepath.Join(root, "tests", "test_widget.py")), fakeRunResult(SuiteResult{Passed: false, Output: out, Err: "exit status 2"}))

	for _, want := range []string{"NOT TESTED", "`pyseto`", "NOT tested"} {
		if !strings.Contains(got, want) {
			t.Errorf("line %q lacks %q", got, want)
		}
	}
	if logged := gateLogText(t, cfg); !strings.Contains(logged, "env-missing") {
		t.Errorf("gate.log lacks the env-missing verdict:\n%s", logged)
	}
}
