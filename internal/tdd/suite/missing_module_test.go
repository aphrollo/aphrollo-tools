package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// collectionFailure is pytest -q output for a suite that never got past
// collection because the interpreter lacks a third-party module, in the shape
// of the report behind issue #1223.
const collectionFailure = `==================================== ERRORS ====================================
____________________ ERROR collecting tests/test_vault.py _____________________
ImportError while importing test module '/r/backend/tests/test_vault.py'.
Traceback:
app/vault.py:3: in <module>
    import pyseto
E   ModuleNotFoundError: No module named 'pyseto'
=========================== short test summary info ============================
ERROR tests/test_vault.py
!!!!!!!!!!!!!!!!!!! Interrupted: 31 errors during collection !!!!!!!!!!!!!!!!!!!!
1 warning, 31 errors in 18.67s
`

func pytestUnderVenv() Runner {
	return Runner{Cmd: "/r/.venv/bin/python", Args: []string{"-m", "pytest", "-q"}}
}

func TestPytestMissingModule_NamesAThirdPartyModuleACollectionFailureLacked(t *testing.T) {
	t.Parallel()
	got := pytestMissingModule(pytestUnderVenv(), t.TempDir(), SuiteResult{Output: collectionFailure})
	if got != "pyseto" {
		t.Errorf("got %q, want pyseto", got)
	}
}

func TestPytestMissingModule_ASubmoduleNamesItsTopLevelPackage(t *testing.T) {
	t.Parallel()
	out := strings.ReplaceAll(collectionFailure, "'pyseto'", "'google.cloud.kms'")
	if got := pytestMissingModule(Runner{Cmd: "pytest", Args: []string{"-q"}}, t.TempDir(), SuiteResult{Output: out}); got != "google" {
		t.Errorf("got %q, want google", got)
	}
}

// A module the repo itself ships that fails to import is a real defect of the
// code under test, never an environment gap.
func TestPytestMissingModule_TheRepoOwnPackageIsARealRed(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"pyseto/__init__.py", "src/pyseto/__init__.py", "pyseto.py"} {
		root := t.TempDir()
		p := filepath.Join(root, filepath.FromSlash(layout))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{Output: collectionFailure}); got != "" {
			t.Errorf("layout %s: got %q, want the repo's own module left a red", layout, got)
		}
	}
}

// One test that ran means the suite was exercised; a stray import error in it
// is a failure, not a missing environment.
func TestPytestMissingModule_ARunThatExecutedATestIsNotAnEnvironmentGap(t *testing.T) {
	t.Parallel()
	out := collectionFailure + "3 passed, 1 error in 2.00s\n"
	if got := pytestMissingModule(pytestUnderVenv(), t.TempDir(), SuiteResult{Output: out}); got != "" {
		t.Errorf("got %q, want none", got)
	}
}

func TestPytestMissingModule_OnlyAFailedPytestRunQualifies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if got := pytestMissingModule(Runner{Cmd: "go", Args: []string{"test"}}, root, SuiteResult{Output: collectionFailure}); got != "" {
		t.Errorf("go runner: got %q", got)
	}
	if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{Passed: true, Output: collectionFailure}); got != "" {
		t.Errorf("passed run: got %q", got)
	}
	if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{TimedOut: true, Output: collectionFailure}); got != "" {
		t.Errorf("timed-out run: got %q", got)
	}
}

func TestMissingModuleCause_NamesTheModuleTheInterpreterAndTheFix(t *testing.T) {
	t.Parallel()
	got := missingModuleCause(pytestUnderVenv(), "/r/backend", "pyseto")
	for _, want := range []string{"`pyseto`", "/r/.venv/bin/python", "requirements", "build its venv", filepath.Join("/r/backend", ".venv")} {
		if !strings.Contains(got, want) {
			t.Errorf("cause %q lacks %q", got, want)
		}
	}
	bare := missingModuleCause(Runner{Cmd: "pytest"}, "/r", "pyseto")
	if !strings.Contains(bare, "pytest on PATH") {
		t.Errorf("a bare pytest runner: cause %q does not name the PATH interpreter", bare)
	}
}

func TestMissingModuleTerminal_ReadsAsNotTestedWithTheCause(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := t.TempDir()
	home := func(string) string { return "/primary/backend" }
	line := missingModuleTerminal(pytestUnderVenv(), root, home, SuiteResult{Output: collectionFailure})
	for _, want := range []string{"NOT TESTED", "`pyseto`", "NOT tested", filepath.Join("/primary/backend", ".venv")} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if got := missingModuleTerminal(pytestUnderVenv(), root, home, SuiteResult{Output: "1 failed, 2 passed in 1s"}); got != "" {
		t.Errorf("an ordinary failure got the line %q", got)
	}
}

// repoWith is a git repo holding the given files, tracked, and the pytest root
// under it that a run is judged in.
func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	tddtest.GitInit(t, root)
	for rel, body := range files {
		tddtest.Write(t, root, rel, body)
	}
	gitDo(t, root, "add", ".")
	return filepath.Join(root, "backend")
}

// failureNaming is collectionFailure for the given modules, one collection
// error each.
func failureNaming(modules ...string) string {
	var b strings.Builder
	for _, m := range modules {
		fmt.Fprintf(&b, "____ ERROR collecting tests/test_%s.py ____\nE   ModuleNotFoundError: No module named '%s'\n", strings.ReplaceAll(m, ".", "_"), m)
	}
	fmt.Fprintf(&b, "!!!! Interrupted: %d errors during collection !!!!\n%d errors in 1.00s\n", len(modules), len(modules))
	return b.String()
}

// The repo's own package, however it is laid out, that fails to import is a
// real red: backend/app/... is not one of the four fixed layouts a root has.
func TestPytestMissingModule_ATrackedPackageAnywhereInTheRepoIsTheRepoOwn(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := repoWith(t, map[string]string{
		"backend/app/services/vault.py": "import pyseto\n",
		"backend/tests/test_vault.py":   "def test_x():\n    pass\n",
		"libs/shared/util.py":           "",
	})
	for _, own := range []string{"services", "app", "util", "shared"} {
		if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{Output: failureNaming(own)}); got != "" {
			t.Errorf("%s is the repo's own, yet %q was called an environment gap", own, got)
		}
	}
	if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{Output: failureNaming("pyseto")}); got != "pyseto" {
		t.Errorf("a third-party module in the same repo: got %q, want pyseto", got)
	}
}

// One error that names the repo's own module hides nothing behind the
// environment: the run is red even when other errors name third-party ones.
func TestPytestMissingModule_OneOwnModuleErrorAmongThirdPartyOnesIsStillRed(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := repoWith(t, map[string]string{"backend/app/vault.py": ""})
	if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{Output: failureNaming("pyseto", "app.vault", "redis")}); got != "" {
		t.Errorf("mixed own and third-party errors: got %q, want a red", got)
	}
}

// An error of any other kind in the same run is a real failure the missing
// module cannot explain away.
func TestPytestMissingModule_AnotherKindOfCollectionErrorKeepsTheRunRed(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := repoWith(t, map[string]string{"backend/app/vault.py": ""})
	out := failureNaming("pyseto") + "____ ERROR collecting tests/test_b.py ____\nE   SyntaxError: invalid syntax\n"
	if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{Output: out}); got != "" {
		t.Errorf("a syntax error beside a missing module: got %q, want a red", got)
	}
}

// A package that only a virtualenv ships is not the repo's own.
func TestPytestMissingModule_AFileInsideAVirtualenvDoesNotMakeAModuleTheRepoOwn(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := repoWith(t, map[string]string{"backend/.venv/lib/site-packages/pyseto/__init__.py": "", "backend/app/x.py": ""})
	if got := pytestMissingModule(pytestUnderVenv(), root, SuiteResult{Output: failureNaming("pyseto")}); got != "pyseto" {
		t.Errorf("got %q, want pyseto: a venv's package is not repo code", got)
	}
}
