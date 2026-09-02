package tdd

import (
	"strings"
	"testing"
	"time"
)

// TestPrecommit_TimeoutRejectsTheCommit pins the 2026-09-02 decision: a
// commit whose suite did not finish is a commit nobody tested, and that must
// not land silently. The gate used to fail OPEN on a timeout (the same
// reasoning as the post-edit hook: a stopwatch is not a verdict) — but at
// commit time the consequence is different, because the untested code stays
// in history. The edit hook keeps the advisory behaviour; the gate rejects
// and says how to recover.
func TestPrecommit_TimeoutRejectsTheCommit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	timedOut := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: "suite timed out", TimedOut: true, Duration: 90 * time.Second}
	}
	res := Precommit(root, timedOut)

	if !res.Blocked {
		t.Fatal("a commit whose suite never finished must be refused, not landed unverified")
	}
	for _, want := range []string{"90", "retry"} {
		if !strings.Contains(strings.ToLower(res.Message), want) {
			t.Fatalf("message = %q, want it to name the elapsed seconds and the retry hint", res.Message)
		}
	}
}

// TestPostEdit_TimeoutStaysAdvisory pins the other half: the edit hook is the
// one place a timeout is still only a report. Blocking an EDIT on a slow
// suite would wedge the session over a stopwatch.
func TestPostEdit_TimeoutStaysAdvisory(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	got := PostEdit(postPayload("Edit", root+"/widget.go"), func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, TimedOut: true, Duration: time.Second}
	})
	if !strings.Contains(got, "TIMEOUT") {
		t.Fatalf("the edit hook must still report a timeout, got %q", got)
	}
}

// TestMechCache_NeverCachesATimedOutRun pins the cache's half of it: a run
// that was killed proved nothing, so it must not stand in for a green next
// time.
func TestMechCache_NeverCachesATimedOutRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	runs := 0
	timedOut := func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		runs++
		return SuiteResult{Passed: false, TimedOut: true, Duration: time.Second}
	}
	Precommit(root, timedOut)
	Precommit(root, timedOut)
	if runs != 2 {
		t.Fatalf("the suite ran %d times, want 2 — a timed-out run must never be cached as green", runs)
	}
}
