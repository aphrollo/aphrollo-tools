package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestGateVerbs_CarryThePostToolUseFailureHookThatCountsAHandRunRed(t *testing.T) {
	found := false
	for _, v := range GateVerbs() {
		found = found || v == "posttoolusefailure"
	}
	if !found {
		t.Errorf("gate verb table = %v, want it to carry posttoolusefailure", GateVerbs())
	}
	if !strings.Contains(gateUsage, "  posttoolusefailure ") {
		t.Errorf("gate usage does not list posttoolusefailure")
	}
}

// A shell call that failed is a hook the gate answers with nothing: it folds the
// call when the call was a suite run, and says no line of its own.
func TestRun_Gate_PostToolUseFailure_IsSilentAndFailsOpen(t *testing.T) {
	gateConfigDir(t)
	failed := `{"session_id":"s-cli","cwd":` + jsonString(t, t.TempDir()) + `,"hook_event_name":"PostToolUseFailure","tool_name":"Bash",` +
		`"tool_input":{"command":"go test ./..."},"tool_use_id":"toolu_cli","error":"Exit code 1\nFAIL","is_interrupt":false}`
	for name, payload := range map[string]string{"a failed suite": failed, "a malformed payload": "{not json", "an empty one": ""} {
		var out, errb bytes.Buffer
		code := Run([]string{"gate", "posttoolusefailure"}, strings.NewReader(payload), &out, &errb)
		if code != 0 || out.Len() != 0 || errb.Len() != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q, want silence and exit 0", name, code, out.String(), errb.String())
		}
	}
}
