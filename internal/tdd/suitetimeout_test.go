package tdd

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestSuiteTimeout_IsNotARed pins the timeout contract end to end: a suite
// run that outlives its deadline is a distinct TimedOut signal, not a red. A
// timeout says nothing about the code under test — treating it as a failure
// nags the model (PostEdit), false-blocks commits (mechanical), and turns
// fail-first into a coin flip (a timed-out worktree run proves nothing) — so
// every consumer must treat TimedOut as inconclusive, never as RED.
func TestSuiteTimeout_IsNotARed(t *testing.T) {
	// timedOut is the fake runner every consumer subtest injects: the suite
	// was killed at the deadline, so it did not pass — but it did not FAIL.
	timedOut := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: "suite timed out", TimedOut: true}
	}

	t.Run("RunSuite marks an over-deadline run TimedOut", func(t *testing.T) {
		// The sleep lives inside the spawned command, not the test process.
		// The sleeper is invoked DIRECTLY (no cmd.exe / sh wrapper): the
		// deadline kill reaches only the direct child, and an orphaned
		// grandchild would hold the temp dir open past the test's cleanup.
		var r Runner
		if runtime.GOOS == "windows" {
			r = Runner{Cmd: "ping", Args: []string{"-n", "30", "127.0.0.1"}}
		} else {
			r = Runner{Cmd: "sleep", Args: []string{"30"}}
		}
		res := RunSuite(200*time.Millisecond)(r, t.TempDir())
		if res.Passed {
			t.Fatal("a killed run must not report Passed")
		}
		if !res.TimedOut {
			t.Fatal("RunSuite must report TimedOut for a run that outlives the deadline")
		}
	})

	// UPDATED for task A2 (2026-08-15): PostEdit no longer stays silent on a
	// timeout — silence there was indistinguishable from "ran and passed".
	// It must still never look like RED (no "outcome=red"/"red-missing-impl"
	// text): a timeout proves nothing about the code either way.
	t.Run("PostEdit reports TIMEOUT, never RED, on a timed-out run", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := mkProject(t, "go.mod")
		got := PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), timedOut)
		if !strings.Contains(got, "TIMEOUT") {
			t.Fatalf("a timed-out suite must report TIMEOUT, got: %s", got)
		}
		if strings.Contains(got, "outcome=red") {
			t.Fatalf("a timed-out suite must never be reported as RED, got: %s", got)
		}
	})

	// UPDATED 2026-09-02: at COMMIT time a timeout now REJECTS
	// (TestPrecommit_TimeoutRejectsTheCommit) — the untested code would
	// otherwise stay in history. What must still hold here is that it is
	// never reported as a RED suite: the message says "did not finish", not
	// "tests failing".
	t.Run("Precommit calls a timed-out run unfinished, not failed", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeGoRepo(t)
		write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
		gitDo(t, root, "add", ".")
		res := Precommit(root, timedOut)
		if !strings.Contains(res.Message, "did not finish") {
			t.Fatalf("message = %q, want it to say the suite did not finish", res.Message)
		}
		if strings.Contains(strings.ToLower(res.Message), "failing") {
			t.Fatalf("message = %q, want a timeout never reported as a failing suite", res.Message)
		}
	})

	t.Run("failFirstViolated is inconclusive on a timed-out run", func(t *testing.T) {
		root := makeGoRepo(t)
		write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { _ = 1 }\n")
		gitDo(t, root, "add", ".")
		if _, conclusive, _, _, _ := failFirstViolated(root, []string{"widget_test.go"}, timedOut); conclusive {
			t.Fatal("a timed-out fail-first run must be inconclusive, not a conclusive verdict")
		}
	})
}
