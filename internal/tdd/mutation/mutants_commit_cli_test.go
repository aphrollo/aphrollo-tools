package mutation

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMutantsCommit_UndeclaredSaysSo(t *testing.T) {
	_, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nundercover = true\n")
	var out, errOut bytes.Buffer
	if code := RunMutantsCommit(root, &out, &errOut); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if !strings.Contains(errOut.String(), "mutants-at-commit") {
		t.Errorf("stderr = %q, want it to say the repo declares no mutants-at-commit", errOut.String())
	}
}

func TestRunMutantsCommit_ASurvivorIsExitOne(t *testing.T) {
	_, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = \"block\"\n")
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	var out, errOut bytes.Buffer
	var code int
	stderr := captureStderr(t, func() { code = RunMutantsCommit(root, &out, &errOut) })
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "gate/gate.go:4:7: CONDITIONALS_BOUNDARY") {
		t.Errorf("stderr = %q, want the survivor named", stderr)
	}
}

func TestRunMutantsCommit_CaughtMutantsAreExitZero(t *testing.T) {
	_, root := commitStage(t, "")
	scriptGo(t, killsUnderTheMutant)
	var out, errOut bytes.Buffer
	if code := RunMutantsCommit(root, &out, &errOut); code != 0 {
		t.Errorf("exit %d, want 0: %s", code, out.String())
	}
}

// ratchet: test_removed TestParseTestedDirs_RelativeSortedAndInsideTheRepo: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
// ratchet: test_removed TestParseTestedDirs_LeavesOutTestDataAndRatchetFixtures: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
// ratchet: test_removed TestRunMutantsTestMap_BuildsTheNamedPackages: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
// ratchet: test_removed TestRunMutantsTestMap_UndeclaredIsInert: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
// ratchet: test_removed TestRunMutantsTestMap_NoHeadroomBuildsNothing: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
// ratchet: test_removed TestRunMutantsTestMap_AFailedBuildIsAnError: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
// ratchet: test_removed TestRunMutantsTestMap_WithNoPackagesNamedItTakesEveryTestedOne: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
