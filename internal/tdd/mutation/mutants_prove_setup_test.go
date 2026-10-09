package mutation

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A test that builds its own repository in the proof's copy runs `git add` on
// paths the copy's depth pushes past Windows's limit; git says so and the test
// fails on setup, not on the mutation. Reading that failure as the predicted
// test failing reported a kill nothing had earned.
func TestRunMutantsProve_ASetupFailureOfTheNamedTestIsNotAKill(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	longPath := strings.Repeat("deep/", 60) + "file.txt"
	fakeRun := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Err: "exit status 1", Output: "--- FAIL: TestAdd (0.12s)\n" +
			"    widget_test.go:14: git add: exit status 128: error: open(\"" + longPath + "\"): Filename too long\n" +
			"    fatal: adding files failed\nFAIL\n"}
	}

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(root, "widget.go"), Old: "return a + b", New: "return a - b", WantFail: "TestAdd",
	}, fakeRun, &out, &errb)

	if code != ExitMutantsProveSetupFailed {
		t.Fatalf("exit = %d, want ExitMutantsProveSetupFailed (%d)\nstdout: %s\nstderr: %s", code, ExitMutantsProveSetupFailed, out.String(), errb.String())
	}
	for _, want := range []string{"SETUP FAILED", "Filename too long", "not a kill"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("verdict %q lacks %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "KILLED") {
		t.Errorf("a setup failure was reported as a kill: %s", out.String())
	}
}

// An ordinary failure of the named test is still a kill.
func TestRunMutantsProve_AnOrdinaryFailureStillKills(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	fakeRun := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Err: "exit status 1", Output: "--- FAIL: TestAdd (0.00s)\n    widget_test.go:7: bad\nFAIL\n"}
	}

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(root, "widget.go"), Old: "return a + b", New: "return a - b", WantFail: "TestAdd",
	}, fakeRun, &out, &errb)

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s", code, ExitMutantsProveKilled, out.String())
	}
}

// The copy's git accepts paths past the Windows limit, so a git run inside it
// (a test's own `git add`, the gate's) does not fail on depth.
func TestNewProveSandbox_TheCopyAllowsLongPaths(t *testing.T) {
	lane := laneWithWork(t)
	box, err := newProveSandbox(lane, lane)
	if err != nil {
		t.Fatal(err)
	}
	defer box.remove()

	if got := strings.TrimSpace(gitOutT(t, box.root, "config", "--local", "--get", "core.longpaths")); got != "true" {
		t.Errorf("core.longpaths in the copy = %q, want true", got)
	}
}
