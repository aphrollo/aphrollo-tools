package mutation

import (
	"bytes"
	"context"
	"io"
	"os"
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

func TestRefreshTestMaps_ALeakingBuildKeepsNoMapAndSaysWhatChanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	rec := recordGitWorldChanges(t)
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	tc.compile = func(argv []string) (int, error) {
		leakInto(root)
		return 0, os.WriteFile(valueAfter(argv, "-o"), []byte("binary"), 0o700)
	}
	var log bytes.Buffer

	built, _, err := refreshTestMaps(context.Background(), root, MutantsConfig{}, []string{"internal/p"}, 1, &log)

	if err == nil || !strings.Contains(err.Error(), "changed the git state") || built != 0 {
		t.Errorf("refreshTestMaps = built %d, err %v, want a refusal and no map built", built, err)
	}
	if _, ok := loadTestMap(root, "internal/p"); ok {
		t.Error("the map of a build that reached the real repository was kept")
	}
	if !strings.Contains(log.String(), "+\tkey = 1") {
		t.Errorf("the log does not say what changed:\n%s", log.String())
	}
	if len(rec.runner) != 1 || rec.runner[0] != "test-map build" {
		t.Errorf("recorded runners %v, want one escape for the test-map build", rec.runner)
	}
}

// A leak stops the build: the packages after it are not built in a world that
// has already been changed.
func TestRefreshTestMaps_ALeakStopsTheBuildOfTheRemainingPackages(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	recordGitWorldChanges(t)
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	mustWrite(t, filepath.Join(root, "internal", "q", "q.go"), "package q\n")
	mustWrite(t, filepath.Join(root, "internal", "q", "q_test.go"), "package q\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "q")
	t.Cleanup(setGoListForTest(func(_ context.Context, root, dir string) (string, error) {
		return filepath.Join(root, filepath.FromSlash(dir)) + "|x.go|x_test.go||\n", nil
	}))
	tc.compile = func(argv []string) (int, error) {
		leakInto(root)
		return 0, os.WriteFile(valueAfter(argv, "-o"), []byte("binary"), 0o700)
	}

	built, _, err := refreshTestMaps(context.Background(), root, MutantsConfig{}, []string{"internal/p", "internal/q"}, 1, io.Discard)

	if err == nil || built != 0 {
		t.Errorf("built %d, err %v, want the leak to stop everything", built, err)
	}
	compiles := 0
	for _, call := range tc.calls {
		if call[0] == "go" {
			compiles++
		}
	}
	if compiles != 1 {
		t.Errorf("%d packages were compiled, want 1: the build stops at the leak", compiles)
	}
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
	if !v.Refused || !strings.Contains(v.Message, "changed the git state") || !strings.Contains(v.Message, "+\tkey = 1") {
		t.Errorf("verdict = refused %v, message %q, want a refusal naming the change", v.Refused, v.Message)
	}
	if len(rec.runner) != 1 || rec.runner[0] != "measurement" {
		t.Errorf("recorded runners %v, want one escape for the measurement", rec.runner)
	}
}

func TestMutantsAtCommitStage_ALeakingRunBlocksTheCommitWithWhatChanged(t *testing.T) {
	cfgDir, root := commitStage(t, "")
	rec := recordGitWorldChanges(t)
	scriptGo(t, func(goCall) (int, string) {
		leakInto(root)
		return 0, "ok\tgate\n"
	})

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if !res.Blocked || !strings.Contains(res.Message, "changed the git state") {
		t.Fatalf("the stage let a run that reached the real repository through: blocked %v, message %q", res.Blocked, res.Message)
	}
	if !strings.Contains(stderr, "+\tkey = 1") {
		t.Errorf("stderr does not say what changed:\n%s", stderr)
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
	if !strings.Contains(errb.String(), "changed the git state") || !strings.Contains(errb.String(), "+\tkey = 1") {
		t.Errorf("stderr does not name the change:\n%s", errb.String())
	}
	if strings.Contains(out.String(), "KILLED") {
		t.Errorf("a run that reached the real repository was reported as a kill:\n%s", out.String())
	}
	if len(rec.runner) != 1 || rec.runner[0] != "proof" {
		t.Errorf("recorded runners %v, want one escape for the proof", rec.runner)
	}
}
