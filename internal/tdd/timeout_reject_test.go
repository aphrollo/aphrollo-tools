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

// TestPrecommit_MechanicalTimeoutNamesTheBoxLoad proves #526's wiring at the
// mechanical-stage call site (mechrun.go's own TimedOut branch, which does
// not route through verdictFor): the rejection message carries the sampled
// box load, not just the elapsed seconds. machineLoadSampleFn is stubbed so
// the assertion does not depend on whatever else is running on the box that
// happens to execute this test; quality runners (vet/lint) pass so the
// mechanical suite itself is what times out.
func TestPrecommit_MechanicalTimeoutNamesTheBoxLoad(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	prev := machineLoadSampleFn
	machineLoadSampleFn = func() (int, float64, []procSample, bool) {
		return 4, 55, []procSample{{pid: 999, name: "find.exe", pctOneCore: 90, cpuHours: 2}}, true
	}
	t.Cleanup(func() { machineLoadSampleFn = prev })

	run := func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		return SuiteResult{Passed: false, TimedOut: true, Duration: 90 * time.Second}
	}
	res := Precommit(root, run)

	if !res.Blocked {
		t.Fatal("a commit whose suite never finished must be refused")
	}
	if !strings.Contains(res.Message, "box: 4 cores, load 55%") || !strings.Contains(res.Message, "find.exe") {
		t.Fatalf("message = %q, want it to carry the sampled box load and the foreign process", res.Message)
	}
}

// TestGoCheckStage_TimeoutNamesTheBoxLoad proves #526's wiring at
// verdictFor's shared outcomeTimeout case, the OTHER call site this issue
// touches (vet/lint/fmt/doctest via goCheckStage, clippy/check via
// qualityVerdict). Same stub, exercised directly against goCheckStage rather
// than through the whole Precommit wall.
func TestGoCheckStage_TimeoutNamesTheBoxLoad(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	prev := machineLoadSampleFn
	machineLoadSampleFn = func() (int, float64, []procSample, bool) {
		return 8, 12, []procSample{{pid: 42, name: "rustc.exe", pctOneCore: 75, cpuHours: 1.25}}, true
	}
	t.Cleanup(func() { machineLoadSampleFn = prev })

	run := func(Runner, string) SuiteResult { return SuiteResult{TimedOut: true} }
	got := goCheckStage("precommit", "vet", root, Runner{Cmd: "go", Args: []string{"vet", "./..."}}, run)

	if !got.Blocked {
		t.Fatal("go vet timing out must block the commit")
	}
	if !strings.Contains(got.Message, "box: 8 cores, load 12%") || !strings.Contains(got.Message, "rustc.exe") {
		t.Fatalf("message = %q, want it to carry the sampled box load and the foreign process", got.Message)
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
