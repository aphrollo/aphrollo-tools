package cli

import "testing"

func TestGateVerdictWord_ExitCodeToOutcome(t *testing.T) {
	if got := gateVerdictWord(0); got != "pass" {
		t.Errorf("gateVerdictWord(0) = %q, want pass", got)
	}
	if got := gateVerdictWord(1); got != "blocked" {
		t.Errorf("gateVerdictWord(1) = %q, want blocked", got)
	}
}
