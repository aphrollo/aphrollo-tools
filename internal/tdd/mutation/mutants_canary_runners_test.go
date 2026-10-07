package mutation

import (
	"bytes"
	"context"
	os "os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The canary stands behind each runner that starts test processes: the test-map
// build, the commit-time run, the merge measurement and the proof. A run whose
// test process wrote to the real repository gets its result refused, with what
// changed said and an escape recorded (#1043).

// leakInto is what a test process that reached the real repository does. It
// avoids t.Fatal, since a runner calls it from its own goroutines.
func leakInto(repo string) {
	_ = exec.Command("git", "-C", repo, "config", "leak.key", "1").Run() // stderr-ok: the leak is the point; a failed one shows as a missing refusal
}

func TestMeasureLane_ALeakingRunIsRefusedWithWhatChanged(t *testing.T) {
	rec := recordGitWorldChanges(t)
	lane, base := measurableTorqueLane(t)
	noSurvivors := `{"files":[{"file_name":"torque/torque.go","mutations":[
		{"type":"ARITHMETIC_BASE","status":"KILLED","line":5,"column":16}]}]}`
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		leakInto(lane)
		mustWrite(t, gremlinsReportPath(lane), noSurvivors)
		return 0, nil
	})

	v, err := MeasureLane(lane, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused || !strings.Contains(v.Message, "the repository's config") || strings.Contains(v.Message, "\n") {
		t.Errorf("verdict = refused %v, message %q, want a refusal naming the change", v.Refused, v.Message)
	}
	if len(rec.runner) != 1 || rec.runner[0] != "measurement" {
		t.Errorf("recorded runners %v, want one escape for the measurement", rec.runner)
	}
}

func TestMutantsAtCommitStage_ALeakingRunBlocksTheCommitWithWhatChanged(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfgDir, root := commitStage(t, "")
	rec := recordGitWorldChanges(t)
	scriptGo(t, func(goCall) (int, string) {
		leakInto(root)
		return 0, "ok\tgate\n"
	})

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if !res.Blocked || !strings.Contains(res.Message, "the repository's config") || strings.Contains(res.Message, "\n") {
		t.Fatalf("the stage let a run that reached the real repository through: blocked %v, message %q", res.Blocked, res.Message)
	}
	if strings.Contains(stderr, "the repository's config") {
		t.Errorf("the refusal was printed twice:\n%s", stderr)
	}
	if len(rec.evidence) != 1 || !strings.Contains(rec.evidence[0], "+\tkey = 1") {
		t.Errorf("the escape evidence does not say what changed: %v", rec.evidence)
	}
	if len(rec.runner) != 1 || rec.runner[0] != "commit-time run" {
		t.Errorf("recorded runners %v, want one escape for the commit-time run", rec.runner)
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-refused:git-world-changed") {
		t.Errorf("gate.log = %q, want a mutants-refused:git-world-changed line", log)
	}
}

func TestRunMutantsProve_ALeakingRunProvesNothing(t *testing.T) {
	rec := recordGitWorldChanges(t)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	leaking := func(Runner, string) SuiteResult {
		leakInto(root)
		return SuiteResult{Passed: false, Output: "--- FAIL: TestAdd (0.00s)\nFAIL\n"}
	}

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a + b",
		New:      "return a - b",
		WantFail: "TestAdd",
	}, leaking, &out, &errb)

	if code != ExitMutantsProveRefused {
		t.Fatalf("exit = %d, want ExitMutantsProveRefused (%d)\nstdout: %s\nstderr: %s", code, ExitMutantsProveRefused, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "the repository's config") || strings.Count(errb.String(), "\n") != 1 {
		t.Errorf("stderr is not one line naming the change:\n%s", errb.String())
	}
	if strings.Contains(out.String(), "KILLED") {
		t.Errorf("a run that reached the real repository was reported as a kill:\n%s", out.String())
	}
	if len(rec.runner) != 1 || rec.runner[0] != "proof" {
		t.Errorf("recorded runners %v, want one escape for the proof", rec.runner)
	}
}

// The coverage build runs under the stage's canary: a compile that reached the
// real repository refuses the commit, says what changed, and keeps no map.
func TestMutantsAtCommitStage_ALeakingCoverageBuildBlocksTheCommitAndKeepsNoMap(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, root := commitStage(t, "")
	rec := recordGitWorldChanges(t)
	tc := &fakeToolchain{list: "TestKind_A\n", profiles: map[string]string{"TestKind_A": "mode: set\nx/gate/gate.go:4.2,4.12 1 1\n"}}
	tc.compile = func(argv []string) (int, error) {
		leakInto(root)
		return 0, os.WriteFile(valueAfter(argv, "-o"), []byte("binary"), 0o700)
	}
	prevExec := testMapExecFn
	testMapExecFn = tc.exec
	t.Cleanup(func() { testMapExecFn = prevExec })
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })

	var res GateResult
	captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if !res.Blocked || !strings.Contains(res.Message, "the repository's config") {
		t.Fatalf("the stage let a coverage build that reached the real repository through: blocked %v, message %q", res.Blocked, res.Message)
	}
	if len(rec.runner) != 1 || rec.runner[0] != "commit-time run" {
		t.Errorf("recorded runners %v, want one escape for the commit-time run", rec.runner)
	}
}

// ratchet: test_removed TestRefreshTestMaps_ALeakingBuildKeepsNoMapAndSaysWhatChanged: the coverage build runs under the commit stage's canary now; TestMutantsAtCommitStage_ALeakingCoverageBuildBlocksTheCommitAndKeepsNoMap proves it
// ratchet: test_removed TestRefreshTestMaps_ALeakStopsTheBuildOfTheRemainingPackages: a leak refuses the whole stage, so no package after it is judged
