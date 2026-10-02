package failfirst

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// noInterpreter is a PATH lookup that finds nothing.
func noInterpreter(string) (string, error) { return "", errors.New("not found") }

// onPath is a PATH lookup that finds python3 only.
func onPath(name string) (string, error) {
	if name == "python3" {
		return "/usr/bin/python3", nil
	}
	return "", errors.New("not found")
}

func touchVenvPython(t *testing.T, root string) string {
	t.Helper()
	p := venvPythons(root)[0]
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPytestProofRunner_RunsPytestUnderTheRootsOwnVenvFirst: the proof runs
// `python -m pytest` with the runner's arguments under the root's virtualenv
// when that imports pytest, ahead of the interpreter on PATH.
func TestPytestProofRunner_RunsPytestUnderTheRootsOwnVenvFirst(t *testing.T) {
	root := t.TempDir()
	venv := touchVenvPython(t, root)
	r := Runner{Cmd: "pytest", Args: []string{"-q", "tests/test_a.py"}, Dir: "somewhere"}

	got, why := pytestProofRunner(pytestSearch{root: root}, r, onPath, func(string) error { return nil })
	if why != "" {
		t.Fatalf("unexpected refusal: %s", why)
	}
	if got.Cmd != venv || !slices.Equal(got.Args, []string{"-m", "pytest", "-q", "tests/test_a.py"}) || got.Dir != "somewhere" {
		t.Errorf("got %+v, want %s -m pytest -q tests/test_a.py in somewhere", got, venv)
	}
}

// TestPytestProofRunner_SkipsAnInterpreterWithoutPytest: a venv that lacks
// pytest does not stop the box's python3 that has it from running the proof.
func TestPytestProofRunner_SkipsAnInterpreterWithoutPytest(t *testing.T) {
	root := t.TempDir()
	venv := touchVenvPython(t, root)
	importable := func(p string) error {
		if p == venv {
			return errors.New("No module named pytest")
		}
		return nil
	}
	got, why := pytestProofRunner(pytestSearch{root: root}, Runner{Cmd: "pytest", Args: []string{"-q"}}, onPath, importable)
	if why != "" || got.Cmd != "/usr/bin/python3" {
		t.Fatalf("got %+v, %q; want python3 from PATH", got, why)
	}
}

// TestPytestProofRunner_SaysWhichPieceIsMissing: with no interpreter the
// reason names the root and where it looked; with interpreters that cannot
// import pytest it names the first one and the root. Neither runs anything.
func TestPytestProofRunner_SaysWhichPieceIsMissing(t *testing.T) {
	root := t.TempDir()
	_, why := pytestProofRunner(pytestSearch{root: root}, Runner{Cmd: "pytest"}, noInterpreter, func(string) error { return nil })
	if !strings.Contains(why, "no python interpreter") || !strings.Contains(why, root) {
		t.Errorf("no interpreter: reason %q does not name the missing interpreter and the root", why)
	}
	_, why = pytestProofRunner(pytestSearch{root: root}, Runner{Cmd: "pytest"}, onPath, func(string) error { return errors.New("no pytest") })
	if !strings.Contains(why, "pytest is not importable by /usr/bin/python3") || !strings.Contains(why, root) {
		t.Errorf("no pytest: reason %q does not name the interpreter and the root", why)
	}
}

// TestPytestProofRunner_LeavesAnyOtherRunnerAlone: only a pytest runner is
// rewritten; a go or node runner comes back as it went in, with no lookup.
func TestPytestProofRunner_LeavesAnyOtherRunnerAlone(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	got, why := pytestProofRunner(pytestSearch{root: t.TempDir()}, r, noInterpreter, func(string) error { return errors.New("never asked") })
	if why != "" || got.Cmd != "go" || !slices.Equal(got.Args, r.Args) {
		t.Fatalf("got %+v, %q; want the go runner untouched", got, why)
	}
}

// writeScript makes an executable sh script, for the fake interpreter.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake interpreter is a sh script") // skip-ok: a POSIX shell script stands in for the interpreter
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestPytestImportable_ReflectsTheInterpretersExit: the probe runs the
// interpreter with `-c "import pytest"` and answers by its exit status.
func TestPytestImportable_ReflectsTheInterpretersExit(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "has-pytest")
	bad := filepath.Join(dir, "no-pytest")
	writeScript(t, ok, "#!/bin/sh\n[ \"$1\" = -c ] && [ \"$2\" = \"import pytest\" ]\n")
	writeScript(t, bad, "#!/bin/sh\nexit 1\n")
	if err := pytestImportable(ok); err != nil {
		t.Errorf("an interpreter that imports pytest was refused: %v", err)
	}
	if err := pytestImportable(bad); err == nil {
		t.Error("an interpreter without pytest was accepted")
	}
}

// pytestBackendRepo commits backend/requirements.txt naming pytest and a
// calc.py, gives backend/.venv/bin/python the script body, and stages a
// changed calc.py with a new test for it.
func pytestBackendRepo(t *testing.T, python string) (root, venv string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root = t.TempDir()
	tddtest.GitInit(t, root)
	write(t, root, ".gitignore", ".venv/\n")
	write(t, root, "backend/requirements.txt", "pytest\n")
	write(t, root, "backend/app/calc.py", "def add(a, b):\n    return 0\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	venv = filepath.Join(root, "backend", ".venv", "bin", "python")
	writeScript(t, venv, python)
	write(t, root, "backend/app/calc.py", "def add(a, b):\n    return a + b\n")
	write(t, root, "backend/tests/test_calc.py", "from app.calc import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n")
	gitDo(t, root, "add", "backend/app/calc.py", "backend/tests/test_calc.py")
	return root, venv
}

// TestFailFirstStage_ProvesAPytestRootUnderItsInterpreter: the proof runs the
// staged test as `<venv python> -m pytest -q tests/test_calc.py` in the
// backend root's worktree, red at HEAD.
func TestFailFirstStage_ProvesAPytestRootUnderItsInterpreter(t *testing.T) {
	root, venv := pytestBackendRepo(t, "#!/bin/sh\nexit 0\n")
	backend := filepath.Join(root, "backend")
	var ran []Runner
	run := func(r Runner, _ string) SuiteResult {
		ran = append(ran, r)
		if len(ran) > 1 { // the green half, with the staged change
			return SuiteResult{Passed: true, Output: "1 passed in 0.01s\n"}
		}
		return SuiteResult{Passed: false, Output: "FAILED tests/test_calc.py::test_add - assert 0 == 3\n"}
	}
	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, backend, []string{"backend/tests/test_calc.py"}, []string{"backend/app/calc.py"}, run)
	})
	if res.Blocked {
		t.Fatalf("a red proof was refused:\n%s\n%s", res.Message, stderr)
	}
	if len(ran) == 0 {
		t.Fatalf("the proof ran nothing:\n%s", stderr)
	}
	if got := ran[0]; got.Cmd != venv || !slices.Equal(got.Args, []string{"-m", "pytest", "-q", "tests/test_calc.py"}) {
		t.Errorf("ran %s %v, want %s -m pytest -q tests/test_calc.py", got.Cmd, got.Args, venv)
	}
	if !strings.Contains(stderr, "→ red-proven") || !strings.Contains(stderr, "→ green-proven") {
		t.Errorf("want red-proven then green-proven:\n%s", stderr)
	}
}

// TestFailFirstStage_SaysNotRunWhenNoInterpreterImportsPytest: an interpreter
// that cannot import pytest, with none else on PATH, runs nothing and prints
// a NOT RUN line naming the root and the reason.
func TestFailFirstStage_SaysNotRunWhenNoInterpreterImportsPytest(t *testing.T) {
	root, _ := pytestBackendRepo(t, "#!/bin/sh\nexit 1\n")
	backend := filepath.Join(root, "backend")
	bin := t.TempDir()
	for _, name := range []string{"python3", "python"} {
		writeScript(t, filepath.Join(bin, name), "#!/bin/sh\nexit 1\n")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ran := 0
	run := func(Runner, string) SuiteResult { ran++; return SuiteResult{} }
	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, backend, []string{"backend/tests/test_calc.py"}, []string{"backend/app/calc.py"}, run)
	})
	if res.Blocked || ran != 0 {
		t.Fatalf("blocked=%v ran=%d; a proof that cannot run must neither refuse nor run", res.Blocked, ran)
	}
	if want := "fail-first in " + backend + " → NOT RUN — pytest is not importable by "; !strings.Contains(stderr, want) {
		t.Fatalf("want %q in:\n%s", want, stderr)
	}
}
