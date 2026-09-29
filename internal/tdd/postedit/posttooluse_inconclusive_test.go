package postedit

import (
	"strings"
	"testing"
)

// A run the memory cap ended proved nothing about the code, and is not a slow
// suite: the line says OOM-KILLED at the cap, never TIMEOUT, and it must not
// count toward the timeout streak that would skip the next edit's suite.
func TestPostEditTimedOut_MemoryCapReadsAsOOMKilledAndSkipsTheTimeoutStreak(t *testing.T) {
	state := &sessionState{ByProject: map[string]projectState{}}
	res := SuiteResult{TimedOut: true, Inconclusive: "OOM-KILLED at 11.6 GB"}
	root := t.TempDir()

	got := postEditTimedOut(Runner{Cmd: "go", Args: []string{"test", "./..."}}, root, "abc123", res, state, "")

	if !strings.Contains(got, "OOM-KILLED at 11.6 GB") || !strings.Contains(got, "inconclusive, code NOT tested") {
		t.Fatalf("advisory = %q, want the OOM-KILLED reason and the untested claim", got)
	}
	if strings.Contains(got, "TIMEOUT") {
		t.Fatalf("advisory = %q must never call a cap kill a timeout", got)
	}
	if streak := state.ByProject[root].TimeoutStreak; streak != 0 {
		t.Fatalf("timeout streak = %d after a cap kill, want 0", streak)
	}
}

func TestPostEditTimedOut_APlainTimeoutStillCountsTowardTheStreak(t *testing.T) {
	state := &sessionState{ByProject: map[string]projectState{}}
	root := t.TempDir()
	got := postEditTimedOut(Runner{Cmd: "go", Args: []string{"test"}}, root, "abc123", SuiteResult{TimedOut: true}, state, "")
	if !strings.Contains(got, "TIMEOUT") || state.ByProject[root].TimeoutStreak != 1 {
		t.Fatalf("advisory=%q streak=%d, want a TIMEOUT line and streak 1", got, state.ByProject[root].TimeoutStreak)
	}
}

func TestInconclusiveVerdict_NamesTheCauseInTheGateLog(t *testing.T) {
	if got := inconclusiveVerdict(SuiteResult{Inconclusive: "OOM-KILLED at 4.0 GB"}); got != "oom-killed" {
		t.Errorf("verdict for a cap kill = %q, want oom-killed", got)
	}
	if got := inconclusiveVerdict(SuiteResult{Inconclusive: "SKIPPED — memory headroom: 1.0 GB available"}); got != "memory-skipped" {
		t.Errorf("verdict for a headroom refusal = %q, want memory-skipped", got)
	}
}

// The detached phase reports what the cap did through its result file, and
// the harvest turns it back into the inconclusive run it was.
func TestPhaseSuiteResult_CarriesTheCapKillAsAnInconclusiveRun(t *testing.T) {
	j := DeferredJob{Dir: t.TempDir(), Project: t.TempDir()}
	res := phaseSuiteResult(j, PhaseOutcome{ExitCode: 137, Seconds: 3, Inconclusive: "OOM-KILLED at 11.6 GB"})
	if res.Passed || !res.TimedOut || res.Inconclusive != "OOM-KILLED at 11.6 GB" {
		t.Fatalf("res = %+v, want an unpassed inconclusive run", res)
	}
	plain := phaseSuiteResult(j, PhaseOutcome{ExitCode: 1, Seconds: 3})
	if plain.TimedOut || plain.Inconclusive != "" {
		t.Fatalf("a plain failing phase must stay a failure, got %+v", plain)
	}
}

func TestJudgeEditResult_ACapKilledPhaseIsInconclusiveNotRed(t *testing.T) {
	root := t.TempDir()
	res := SuiteResult{TimedOut: true, Inconclusive: "OOM-KILLED at 11.6 GB", Output: "signal: killed\nFAIL\tpkg\t1.2s\n"}
	got := judgeEditResult(Runner{Cmd: "go", Args: []string{"test", "./..."}}, "a.go", "e1", res, root, nil, "", "abc123")
	if !strings.Contains(got, "OOM-KILLED at 11.6 GB") || strings.Contains(got, "TIMEOUT") {
		t.Fatalf("judged = %q, want the OOM-KILLED line and no timeout wording", got)
	}
}
