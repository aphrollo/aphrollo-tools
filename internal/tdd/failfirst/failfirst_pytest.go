package failfirst

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A pytest root's fail-first proof runs in a worktree at HEAD, where the
// gitignored virtualenv of the root is absent, and `pytest` on PATH may be a
// different Python's. So the proof runs `python -m pytest` under an
// interpreter that is known to import pytest: the root's own .venv or venv,
// then the same root's venv in the repo's other worktrees (the primary checkout, then the lane a merge is judging), else the box's python3 or python. When none can, the proof says which
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

// pytestSearch is where a pytest root looks for its interpreter: the root's
// own virtualenvs first, then those of the same root in other worktrees of its
// repo (elsewhere, in order), then the box's python. remedy is the root whose
// .venv the reason tells the user to create; "" means root itself.
type pytestSearch struct {
	root      string
	elsewhere []string
	remedy    string
}

// venvDirs are the virtualenv directories the search looks in, in order.
func (s pytestSearch) venvDirs() []string {
	var out []string
	for _, r := range append([]string{s.root}, s.elsewhere...) {
		out = append(out, filepath.Join(r, ".venv"), filepath.Join(r, "venv"))
	}
	return out
}

// pytestProofRunner is r, when it is a pytest runner (any other runner comes
// back unchanged), rewritten to run under the first
// interpreter that imports pytest with the same arguments (`python -m pytest
// -q <tests>`), or why none can. The interpreter is only a program: the
// runner keeps its own Dir, so the tests run against the code in that
// directory whichever tree the venv sits in. look finds an interpreter on
// PATH and importable says whether one has pytest.
func pytestProofRunner(search pytestSearch, r Runner, look func(string) (string, error), importable func(string) error) (Runner, string) {
	if r.Cmd != "pytest" {
		return r, ""
	}
	var found []string
	for _, root := range append([]string{search.root}, search.elsewhere...) {
		for _, p := range venvPythons(root) {
			if _, err := os.Stat(p); err == nil {
				found = append(found, p)
			}
		}
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := look(name); err == nil {
			found = append(found, p)
		}
	}
	remedy := cmp.Or(search.remedy, search.root)
	create := fmt.Sprintf("create %s with the requirements of %s installed", filepath.Join(remedy, ".venv"), search.root)
	searched := strings.Join(search.venvDirs(), ", ")
	if len(found) == 0 {
		return Runner{}, fmt.Sprintf("no python interpreter is on PATH or in any of %s; %s so the gate can run pytest", searched, create)
	}
	for _, p := range found {
		if importable(p) == nil {
			return Runner{Cmd: p, Args: append([]string{"-m", "pytest"}, r.Args...), Dir: r.Dir}, ""
		}
	}
	return Runner{}, fmt.Sprintf("pytest is not importable by %s; searched virtualenvs in %s; %s so the gate can run it (the system python is externally managed)", found[0], searched, create)
}

// pytestExecRunner is pytestProofRunner over the real PATH and interpreters,
// searching the same root in the repo's other worktrees too.
func pytestExecRunner(root string, r Runner) (Runner, string) {
	if r.Cmd != "pytest" {
		return r, ""
	}
	return pytestProofRunner(otherWorktreeRoots(root), r, exec.LookPath, pytestImportable)
}
