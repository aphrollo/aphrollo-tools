package failfirst

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

	got, why := pytestProofRunner(root, r, onPath, func(string) error { return nil })
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
	got, why := pytestProofRunner(root, Runner{Cmd: "pytest", Args: []string{"-q"}}, onPath, importable)
	if why != "" || got.Cmd != "/usr/bin/python3" {
		t.Fatalf("got %+v, %q; want python3 from PATH", got, why)
	}
}

// TestPytestProofRunner_SaysWhichPieceIsMissing: with no interpreter the
// reason names the root and where it looked; with interpreters that cannot
// import pytest it names the first one and the root. Neither runs anything.
func TestPytestProofRunner_SaysWhichPieceIsMissing(t *testing.T) {
	root := t.TempDir()
	_, why := pytestProofRunner(root, Runner{Cmd: "pytest"}, noInterpreter, func(string) error { return nil })
	if !strings.Contains(why, "no python interpreter") || !strings.Contains(why, root) {
		t.Errorf("no interpreter: reason %q does not name the missing interpreter and the root", why)
	}
	_, why = pytestProofRunner(root, Runner{Cmd: "pytest"}, onPath, func(string) error { return errors.New("no pytest") })
	if !strings.Contains(why, "pytest is not importable by /usr/bin/python3") || !strings.Contains(why, root) {
		t.Errorf("no pytest: reason %q does not name the interpreter and the root", why)
	}
}
