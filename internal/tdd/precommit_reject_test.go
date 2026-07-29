package tdd

import (
	"os"
	"strings"
	"testing"
)

// TestPrecommit_MechanicalRejectionNamesTheFailure pins the visibility contract
// for a mechanical block: a gate that says "tests failing" must say WHAT. The
// observed failure mode was the opposite — a ~2000-char HEAD snippet of a long
// suite showed only green `ok` lines while the actual failure sat at the tail,
// so the operator could not tell a real red from a phantom one.
func TestPrecommit_MechanicalRejectionNamesTheFailure(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	// A long green preamble followed by the one failure: head-truncation at
	// 2000 chars would show only `ok` lines and hide the FAIL entirely.
	fullOutput := strings.Repeat("test ok_case_padding ... ok\n", 200) +
		"--- FAIL: TestWidget\n    widget_test.go:9: boom\nFAIL\n"
	red := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: fullOutput, Err: "exit status 1"}
	}

	res := Precommit(root, red)
	if !res.Blocked {
		t.Fatal("a red mechanical run must block")
	}
	if !strings.Contains(res.Message, "TestWidget") {
		t.Fatalf("the rejection must name the failing test, got:\n%s", res.Message)
	}
	if !strings.Contains(res.Message, "boom") {
		t.Fatalf("the rejection snippet must include the TAIL of the output (where the failure detail lives), got:\n%s", res.Message)
	}

	logPath := mechRejectLogPath()
	if logPath == "" {
		t.Fatal("with a state dir present the rejection must persist a full log")
	}
	if !strings.Contains(res.Message, logPath) {
		t.Fatalf("the rejection must point at the full log %s, got:\n%s", logPath, res.Message)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("full log must exist: %v", err)
	}
	if !strings.Contains(string(data), fullOutput) {
		t.Fatal("the persisted log must carry the FULL untruncated runner output")
	}
}

// A red run whose output names no test (a link error, a wedged harness, a
// runner-level failure) must surface the runner error instead of a bare
// "failing" with no subject.
func TestPrecommit_MechanicalRejectionSurfacesRunnerError(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	red := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: "error: linking with `link.exe` failed\n", Err: "exit status 101"}
	}
	res := Precommit(root, red)
	if !res.Blocked {
		t.Fatal("a red mechanical run must block")
	}
	if !strings.Contains(res.Message, "exit status 101") {
		t.Fatalf("with no parseable failing test the rejection must carry the runner error, got:\n%s", res.Message)
	}
}
