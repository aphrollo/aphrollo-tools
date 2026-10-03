package guardrail

import (
	"encoding/json"
	"os"
	"testing"
)

// The recorded PreToolUse payloads of the real harness go through the policy
// hook's own decoder: the shell call is judged by its tool name and its
// tool_input.command, and a Write, which carries no command, is let through.
const recordedHookDir = "../tdd/internal/tddtest/testdata/hooks/"

func recordedHook(t *testing.T, file string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(recordedHookDir + file)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDecideFromHookInput_AllowsTheRecordedBashAndWriteCalls(t *testing.T) {
	for _, file := range []string{"pretooluse_bash.json", "pretooluse_write.json"} {
		raw, err := json.Marshal(recordedHook(t, file))
		if err != nil {
			t.Fatal(err)
		}

		got, err := DecideFromHookInput(raw)

		if err != nil || got.Action != Allow {
			t.Errorf("%s: action %v, err %v, want Allow and no error", file, got.Action, err)
		}
	}
}

func TestDecideFromHookInput_ReadsTheCommandFromTheRecordedShape(t *testing.T) {
	m := recordedHook(t, "pretooluse_bash.json")
	m["tool_input"].(map[string]any)["command"] = "sleep 30"
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	got, err := DecideFromHookInput(raw)

	if err != nil || got.Action != Block {
		t.Fatalf("action %v, err %v, want Block: the recorded payload names its command in tool_input.command", got.Action, err)
	}
}

// recordedBashPayloadWith is the recorded shell payload with its command set
// and, when background is non-nil, tool_input.run_in_background set to it.
func recordedBashPayloadWith(t *testing.T, command string, background *bool) []byte {
	t.Helper()
	m := recordedHook(t, "pretooluse_bash.json")
	in := m["tool_input"].(map[string]any)
	in["command"] = command
	if background != nil {
		in["run_in_background"] = *background
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A background call blocks nothing, so the foreground wait rules stay silent
// for it: the harness marks it with tool_input.run_in_background (#1170).
func TestDecideFromHookInput_BackgroundCallsAreNotForegroundWaits(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name       string
		command    string
		background *bool
		want       Action
	}{
		{"sleep in the background", "sleep 45; aphrollo workspace merge 1169 --wait", &yes, Allow},
		{"watch in the background", "gh pr checks 1169 --watch", &yes, Allow},
		{"sleep with the field false", "sleep 45; aphrollo workspace merge 1169 --wait", &no, Block},
		{"sleep with the field absent", "sleep 45; aphrollo workspace merge 1169 --wait", nil, Block},
		{"watch with the field false", "gh pr checks 1169 --watch", &no, Block},
		{"noisy output still warns in the background", "pytest tests", &yes, Warn},
	}
	for _, c := range cases {
		got, err := DecideFromHookInput(recordedBashPayloadWith(t, c.command, c.background))
		if err != nil || got.Action != c.want {
			t.Errorf("%s: action %v, err %v, want %v", c.name, got.Action, err, c.want)
		}
	}
}

// A background python REPL still spins at 100% CPU, so the stdin rule is not
// a foreground-wait rule and holds for a background call too.
func TestDecideFromHookInput_BackgroundPythonOnNullStdinIsStillBlocked(t *testing.T) {
	withNullStdinTTY(t, true)
	yes := true

	got, err := DecideFromHookInput(recordedBashPayloadWith(t, "python - < /dev/null", &yes))

	if err != nil || got.Action != Block {
		t.Fatalf("action %v, err %v, want Block", got.Action, err)
	}
}
