package precommit

import (
	"strings"
	"testing"
	"time"
)

// A commit whose mechanical suite was ended by the memory cap is as untested
// as one that timed out, so it is refused — but as an OOM-KILLED run, with the
// cap, and never in a timeout's words (issue #1005).
func TestPrecommit_MechanicalCapKillIsRefusedAsOOMKilledNotATimeout(t *testing.T) {
	t.Parallel()
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	run := func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		return SuiteResult{TimedOut: true, Inconclusive: "OOM-KILLED at 11.6 GB", Duration: 12 * time.Second}
	}
	var res GateResult
	captureGate(t, func() { res = Mechanical(root, run) })

	if !res.Blocked {
		t.Fatal("a commit whose suite the cap ended tested nothing and must be refused")
	}
	if !strings.Contains(res.Message, "OOM-KILLED at 11.6 GB") || !strings.Contains(res.Message, "memory-cap") {
		t.Fatalf("message = %q, want the cap kill and the memory-cap remedy", res.Message)
	}
	if strings.Contains(res.Message, "did not finish") || strings.Contains(strings.ToUpper(res.Message), "TIMEOUT") {
		t.Fatalf("message = %q must not read as a timeout", res.Message)
	}
	requireVerdictHere(t, "inconclusive-rejected")
}

func TestGoCheckStage_CapKillIsRefusedAsOOMKilledNotATimeout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	run := func(Runner, string) SuiteResult {
		return SuiteResult{TimedOut: true, Inconclusive: "OOM-KILLED at 4.0 GB", Duration: 5 * time.Second}
	}
	var got GateResult
	captureGate(t, func() {
		got = goCheckStage("precommit", "vet", root, Runner{Cmd: "go", Args: []string{"vet", "./..."}}, run)
	})
	if !got.Blocked || !strings.Contains(got.Message, "OOM-KILLED at 4.0 GB") || strings.Contains(got.Message, "did not finish") {
		t.Fatalf("got blocked=%v message %q, want a refusal naming the cap kill", got.Blocked, got.Message)
	}
	requireVerdictHere(t, "inconclusive-rejected")
}

func TestQuietUnfinished_NamesTheCapKillOrTheMissingVerdict(t *testing.T) {
	t.Parallel()
	kill := quietUnfinished("precommit", "clippy -p crate in /r", SuiteResult{Inconclusive: "OOM-KILLED at 2.0 GB"})
	if !strings.Contains(kill, "ended as OOM-KILLED at 2.0 GB") {
		t.Errorf("cap kill message = %q", kill)
	}
	plain := quietUnfinished("precommit", "clippy -p crate in /r", SuiteResult{})
	if !strings.Contains(plain, "did not finish") {
		t.Errorf("plain unfinished message = %q", plain)
	}
}
