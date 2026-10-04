package failfirst

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// countingProbe is an import probe that says yes and counts its runs.
func countingProbe(n *int) func(string) error {
	return func(string) error { *n++; return nil }
}

// A second resolution of the same root, with nothing changed, starts no
// python: the first one's answer is read back.
func TestPytestCachedRunner_ASecondResolutionSpawnsNoPython(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	want := writeFileAt(t, filepath.Join(root, ".venv", "bin", "python"))
	probes := 0
	for i := range 3 {
		got, why := pytestCachedRunner(state, pytestSearch{root: root}, Runner{Cmd: "pytest", Args: []string{"-q"}}, noInterpreter, countingProbe(&probes))
		if why != "" || got.Cmd != want || got.Args[0] != "-m" {
			t.Fatalf("resolution %d: got %+v, %q; want %s -m pytest", i, got, why, want)
		}
	}
	if probes != 1 {
		t.Errorf("python was probed %d times over 3 resolutions, want 1", probes)
	}
}

// A venv that appears ahead of the cached one, or whose directory changes,
// invalidates the answer: the next resolution probes again and finds it.
func TestPytestCachedRunner_ANewVenvAheadOfTheCachedOneIsProbedAndWins(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	older := writeFileAt(t, filepath.Join(root, "env", "bin", "python"))
	probes := 0
	got, _ := pytestCachedRunner(state, pytestSearch{root: root}, Runner{Cmd: "pytest"}, noInterpreter, countingProbe(&probes))
	if got.Cmd != older {
		t.Fatalf("first resolution chose %s, want %s", got.Cmd, older)
	}
	newer := writeFileAt(t, filepath.Join(root, ".venv", "bin", "python"))

	got, why := pytestCachedRunner(state, pytestSearch{root: root}, Runner{Cmd: "pytest"}, noInterpreter, countingProbe(&probes))
	if why != "" || got.Cmd != newer || probes != 2 {
		t.Errorf("after a venv was built ahead: got %s, %q after %d probes; want %s after 2", got.Cmd, why, probes, newer)
	}
}

// A venv directory whose modification time moved (the venv was rebuilt under
// the same name) is probed again even though its path is unchanged.
func TestPytestCachedRunner_ARebuiltVenvDirectoryIsProbedAgain(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	py := writeFileAt(t, filepath.Join(root, ".venv", "bin", "python"))
	probes := 0
	pytestCachedRunner(state, pytestSearch{root: root}, Runner{Cmd: "pytest"}, noInterpreter, countingProbe(&probes))
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, ".venv"), later, later); err != nil {
		t.Fatal(err)
	}

	pytestCachedRunner(state, pytestSearch{root: root}, Runner{Cmd: "pytest"}, noInterpreter, countingProbe(&probes))
	if probes != 2 {
		t.Errorf("a changed venv directory was answered from the cache (%d probes for %s)", probes, py)
	}
}

// A refusal is never remembered, so fixing the environment is seen at once.
func TestPytestCachedRunner_ARefusalIsNotCached(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	if _, why := pytestCachedRunner(state, pytestSearch{root: root}, Runner{Cmd: "pytest"}, noInterpreter, func(string) error { return nil }); why == "" {
		t.Fatal("no interpreter exists, yet one was found")
	}
	want := writeFileAt(t, filepath.Join(root, ".venv", "bin", "python"))
	got, why := pytestCachedRunner(state, pytestSearch{root: root}, Runner{Cmd: "pytest"}, noInterpreter, func(string) error { return nil })
	if why != "" || got.Cmd != want {
		t.Errorf("got %s, %q; want %s", got.Cmd, why, want)
	}
}
