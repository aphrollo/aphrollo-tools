package precommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// useRealPytestProbe restores the merge gate's real interpreter probe, which
// the package's TestMain stubs out, for a test of the probe itself.
func useRealPytestProbe(t *testing.T) {
	t.Helper()
	prev := pytestResolveFn
	pytestResolveFn = pytestExecRunner
	t.Cleanup(func() { pytestResolveFn = prev })
}

// TestGateRoot_MergeRunsAPytestRootUnderAnInterpreterThatImportsPytest is
// issue #1002: the merge gate ran a bare `pytest` from PATH, so a box whose
// pytest lives in the root's virtualenv failed the merge with a raw exec
// error. It runs `python -m pytest` under the first interpreter that imports
// pytest, as the commit gate's proof does.
// Serial: swaps the package-level pytest probe (pytestResolveFn).
func TestGateRoot_MergeRunsAPytestRootUnderAnInterpreterThatImportsPytest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	useRealPytestProbe(t)
	root := makeBackendPytestRepo(t, fakePython)
	groups := stagedRootGroups(root)

	var res GateResult
	stderr := captureStderr(t, func() {
		res = gateRoot("premerge", root, groups[0], RunSuite(precommitTestTimeout), false)
	})
	if res.Blocked {
		t.Fatalf("a suite that passes under the root's interpreter refused the merge:\n%s\n%s", res.Message, stderr)
	}
	want := filepath.Join(".venv", "bin", "python") + " -m pytest -q in " + filepath.Join(root, "backend")
	if !strings.Contains(stderr, want) {
		t.Fatalf("the merge did not run pytest under the root's interpreter; want %q in:\n%s", want, stderr)
	}
}

// TestGateRoot_MergeSaysNotRunWhenNoInterpreterImportsPytest: with no
// interpreter able to import pytest, the merge gate says NOT RUN with the
// reason and refuses the merge without starting a run, rather than reading a
// missing runner as a suite failure.
// Serial: swaps the package-level pytest probe (pytestResolveFn) and sets the process-wide env var PATH.
func TestGateRoot_MergeSaysNotRunWhenNoInterpreterImportsPytest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	useRealPytestProbe(t)
	root := makeBackendPytestRepo(t, "#!/bin/sh\nexit 1\n")
	groups := stagedRootGroups(root)
	bin := t.TempDir()
	for _, name := range []string{"python3", "python"} {
		if err := proc.WriteExecutable(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var res GateResult
	stderr := captureStderr(t, func() {
		res = gateRoot("premerge", root, groups[0], RunSuite(precommitTestTimeout), false)
	})
	want := "gate premerge: pytest in " + filepath.Join(root, "backend") + " → NOT RUN — pytest is not importable by "
	if !strings.Contains(stderr, want) {
		t.Fatalf("no NOT RUN line naming the root and the reason; want %q in:\n%s", want, stderr)
	}
	if !res.Blocked || !strings.Contains(res.Message, "NOT RUN") || !strings.Contains(res.Message, "pytest is not importable") {
		t.Fatalf("the merge cannot be judged without the suite and must say so; got blocked=%v %q", res.Blocked, res.Message)
	}
	if strings.Contains(stderr, "→ blocked") || strings.Contains(stderr, "executable file not found") {
		t.Fatalf("a missing runner was read as a suite failure:\n%s", stderr)
	}
}

// TestPytestSuiteRunner_AMergeCheckoutRunsUnderThePrimaryCheckoutsVenv: the
// pre-merge gate judges a fresh merge worktree, which has no gitignored
// backend/.venv. The root's interpreter comes from the primary checkout's
// venv, and the suite still runs in the merge worktree's own backend.
// Serial: swaps the package-level pytest probe (pytestResolveFn) and sets the process-wide env var PATH.
func TestPytestSuiteRunner_AMergeCheckoutRunsUnderThePrimaryCheckoutsVenv(t *testing.T) {
	useRealPytestProbe(t)
	bin := t.TempDir()
	if err := proc.WriteExecutable(filepath.Join(bin, "python3"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	primary := makeBackendPytestRepo(t, fakePython)
	merge := filepath.Join(t.TempDir(), "gate-prmerge-1")
	gitDo(t, primary, "worktree", "add", "-q", "--detach", merge, "HEAD")
	backend := filepath.Join(merge, "backend")

	got, refused := pytestSuiteRunner("premerge", backend, Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: backend})
	if refused.Blocked {
		t.Fatalf("a merge checkout whose primary has a venv with pytest was refused: %s", refused.Message)
	}
	if want := filepath.Join(primary, "backend", ".venv", "bin", "python"); got.Cmd != want || got.Dir != backend {
		t.Errorf("ran %s in %s, want %s in %s", got.Cmd, got.Dir, want, backend)
	}
}

// collectionMissingModule is pytest's report for a suite whose interpreter
// lacks a third-party module (issue #1223).
const collectionMissingModule = `ERROR collecting tests/test_vault.py
E   ModuleNotFoundError: No module named 'pyseto'
!!!!!!!!!!!!!!!!!!! Interrupted: 31 errors during collection !!!!!!!!!!!!!!!!!!!!
1 warning, 31 errors in 18.67s
`

// A pytest run that failed only because its python lacks a third-party
// module is a suite that was not run: the merge is refused as NOT TESTED with
// the cause and the fix, and the failure is never reported as a failing test.
func TestGateRoot_MergeRefusesAMissingModuleAsNotTestedNeverAsAFailedTest(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "backend/requirements.txt", "pyseto\npytest==8.2\n")
	write(t, root, "backend/app/calc.py", "def add(a, b):\n    return a + b\n")
	write(t, root, "backend/tests/test_calc.py", "from app.calc import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n")
	gitDo(t, root, "add", ".")
	groups := stagedRootGroups(root)
	run := func(r Runner, _ string) SuiteResult {
		return SuiteResult{Passed: false, Output: collectionMissingModule, Err: "exit status 2"}
	}

	var res GateResult
	stderr := captureStderr(t, func() { res = gateRoot("premerge", root, groups[0], run, false) })

	if !res.Blocked {
		t.Fatalf("a suite that never ran let the merge through: %q", res.Message)
	}
	for _, want := range []string{"NOT TESTED", "`pyseto`", "build its venv"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal %q lacks %q", res.Message, want)
		}
	}
	if strings.Contains(stderr, "→ blocked") {
		t.Errorf("a missing environment was reported as a failed suite:\n%s", stderr)
	}
	// One line per refusal, not the stage's own and the verdict's.
	if n := strings.Count(stderr, "NOT TESTED"); n != 1 {
		t.Errorf("the refusal was printed %d times, want once:\n%s", n, stderr)
	}
	logged := gateLogText(t, cfg)
	if !strings.Contains(logged, "env-missing-rejected") || strings.Contains(logged, "check-error-rejected") {
		t.Errorf("gate.log wants env-missing-rejected, not the generic check error:\n%s", logged)
	}
}

// A repo's own module that fails to import is a real failure, still a block
// and still reported as one.
func TestGateRoot_MergeStillBlocksAsAFailedTestWhenTheRepoOwnModuleIsMissing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "backend/requirements.txt", "pytest==8.2\n")
	write(t, root, "backend/pyseto/__init__.py", "")
	write(t, root, "backend/tests/test_calc.py", "def test_add():\n    assert True\n")
	gitDo(t, root, "add", ".")
	groups := stagedRootGroups(root)
	run := func(r Runner, _ string) SuiteResult {
		return SuiteResult{Passed: false, Output: collectionMissingModule, Err: "exit status 2"}
	}

	var res GateResult
	stderr := captureStderr(t, func() { res = gateRoot("premerge", root, groups[0], run, false) })

	if !res.Blocked || strings.Contains(res.Message, "NOT TESTED") || !strings.Contains(stderr, "→ blocked") {
		t.Fatalf("blocked=%v message=%q stderr:\n%s\nwant a plain failed-suite block", res.Blocked, res.Message, stderr)
	}
}

// The merge gate judges a throwaway worktree, which no one keeps a venv in:
// the refusal names the venv to build in the primary checkout, where the
// resolver searches and the user works, never the merge worktree.
func TestGateRoot_MergeNamesThePrimaryCheckoutsVenvNotTheThrowawayWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	primary := t.TempDir()
	gitInit(t, primary)
	write(t, primary, "backend/requirements.txt", "pyseto\npytest==8.2\n")
	gitDo(t, primary, "add", ".")
	gitDo(t, primary, "commit", "-qm", "base")
	merge := filepath.Join(t.TempDir(), "gate-prmerge-1")
	gitDo(t, primary, "worktree", "add", "-q", "--detach", merge, "HEAD")
	write(t, merge, "backend/tests/test_calc.py", "def test_add():\n    assert True\n")
	gitDo(t, merge, "add", ".")
	groups := stagedRootGroups(merge)
	run := func(r Runner, _ string) SuiteResult {
		return SuiteResult{Passed: false, Output: collectionMissingModule, Err: "exit status 2"}
	}

	var res GateResult
	captureStderr(t, func() { res = gateRoot("premerge", merge, groups[0], run, false) })

	if want := filepath.Join(primary, "backend", ".venv"); !res.Blocked || !strings.Contains(res.Message, want) {
		t.Errorf("blocked=%v, message %q does not name %s", res.Blocked, res.Message, want)
	}
	if strings.Contains(res.Message, filepath.Join(merge, "backend", ".venv")) {
		t.Errorf("message names the throwaway merge worktree's venv: %q", res.Message)
	}
}
