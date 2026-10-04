package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	line := missingModuleTerminal(pytestUnderVenv(), root, SuiteResult{Output: collectionFailure})
	for _, want := range []string{"NOT TESTED", "`pyseto`", "NOT tested"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if got := missingModuleTerminal(pytestUnderVenv(), root, SuiteResult{Output: "1 failed, 2 passed in 1s"}); got != "" {
		t.Errorf("an ordinary failure got the line %q", got)
	}
}
