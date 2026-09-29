package failfirst

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// A pytest root's fail-first proof runs in a worktree at HEAD, where the
// gitignored virtualenv of the root is absent, and `pytest` on PATH may be a
// different Python's. So the proof runs `python -m pytest` under an
// interpreter that is known to import pytest: the root's own .venv or venv,
// else the box's python3 or python. When none can, the proof says which
// piece is missing — no interpreter, or no pytest in the ones found — and
// runs nothing: an unrunnable proof is named NOT RUN, never a silent skip.

// pytestImportProbeTimeout bounds the one process the probe starts.
const pytestImportProbeTimeout = time.Minute

// pytestImportable reports whether the interpreter can import pytest.
func pytestImportable(python string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pytestImportProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, python, "-c", "import pytest").Run()
}

// venvPythons are the interpreters a root's own virtualenv would carry, the
// POSIX layout and the Windows one; only the one the host built exists.
func venvPythons(root string) []string {
	rel := []string{filepath.Join("bin", "python"), filepath.Join("Scripts", "python.exe")}
	var out []string
	for _, venv := range []string{".venv", "venv"} {
		for _, r := range rel {
			out = append(out, filepath.Join(root, venv, r))
		}
	}
	return out
}

// pytestProofRunner is r, when it is a pytest runner (any other runner comes
// back unchanged), rewritten to run under the first
// interpreter that imports pytest with the same arguments (`python -m pytest
// -q <tests>`), or why none can. look finds an interpreter on PATH and
// importable says whether one has pytest.
func pytestProofRunner(root string, r Runner, look func(string) (string, error), importable func(string) error) (Runner, string) {
	if r.Cmd != "pytest" {
		return r, ""
	}
	var found []string
	for _, p := range venvPythons(root) {
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		}
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := look(name); err == nil {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return Runner{}, fmt.Sprintf("no python interpreter is on PATH or in %s or %s; install one so fail-first can run pytest for %s",
			filepath.Join(root, ".venv"), filepath.Join(root, "venv"), root)
	}
	for _, p := range found {
		if importable(p) == nil {
			return Runner{Cmd: p, Args: append([]string{"-m", "pytest"}, r.Args...), Dir: r.Dir}, ""
		}
	}
	return Runner{}, fmt.Sprintf("pytest is not importable by %s; install the requirements of %s there so fail-first can run it", found[0], root)
}

// pytestExecRunner is pytestProofRunner over the real PATH and interpreters.
func pytestExecRunner(root string, r Runner) (Runner, string) {
	return pytestProofRunner(root, r, exec.LookPath, pytestImportable)
}
