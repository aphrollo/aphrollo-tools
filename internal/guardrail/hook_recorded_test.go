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
