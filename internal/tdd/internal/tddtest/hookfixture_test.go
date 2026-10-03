package tddtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The catalog is written by hand; this test holds it to the files, so a
// recording edited or added without its row (or a row with no recording) fails
// here and not in a decoder test that trusts the row.
func TestHookCases_DescribeExactlyTheRecordedFiles(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range HookCases {
		listed[c.File] = true
		t.Run(c.File, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(HookFixture(t, c.File), &m); err != nil {
				t.Fatal(err)
			}
			str := func(key string) string { s, _ := m[key].(string); return s }
			in, _ := m["tool_input"].(map[string]any)
			inStr := func(key string) string { s, _ := in[key].(string); return s }

			if got := str("hook_event_name"); got != c.Event {
				t.Errorf("hook_event_name = %q, want %q", got, c.Event)
			}
			if got := str("session_id"); got != HookSession {
				t.Errorf("session_id = %q, want the scrubbed %q", got, HookSession)
			}
			if got := str("cwd"); got != c.Cwd {
				t.Errorf("cwd = %q, want %q", got, c.Cwd)
			}
			if got := str("tool_name"); got != c.Tool {
				t.Errorf("tool_name = %q, want %q", got, c.Tool)
			}
			if inStr("file_path") != c.FilePath || inStr("command") != c.Command || inStr("content") != c.Content {
				t.Errorf("tool_input = %v, want file_path %q command %q content %q", in, c.FilePath, c.Command, c.Content)
			}
			if str("agent_id") != c.AgentID || str("agent_type") != c.AgentType {
				t.Errorf("agent = %q/%q, want %q/%q", str("agent_id"), str("agent_type"), c.AgentID, c.AgentType)
			}
			if !strings.HasPrefix(str("prompt"), c.PromptPrefix) {
				t.Errorf("prompt = %q, want the prefix %q", str("prompt"), c.PromptPrefix)
			}
			if v, has := m["stop_hook_active"]; has != c.StopHookActive || (has && v != false) {
				t.Errorf("stop_hook_active = %v (present %v), want present=%v and false", v, has, c.StopHookActive)
			}
			var tools []string
			calls, _ := m["tool_calls"].([]any)
			for _, call := range calls {
				name, _ := call.(map[string]any)["tool_name"].(string)
				tools = append(tools, name)
			}
			if strings.Join(tools, ",") != strings.Join(c.BatchTools, ",") {
				t.Errorf("tool_calls tools = %v, want %v", tools, c.BatchTools)
			}
		})
	}
	files, err := filepath.Glob(filepath.Join(hookFixtureDir(t), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if name := filepath.Base(f); !listed[name] {
			t.Errorf("%s is recorded but HookCases has no row for it", name)
		}
	}
}

func TestHookFixtures_CarryNoPersonalData(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(hookFixtureDir(t), "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings found (err %v)", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"olive", "905e65cd", "60cdc48e", "37cc34ad", "AppData"} {
			if strings.Contains(string(raw), leak) {
				t.Errorf("%s still carries %q: scrub it", filepath.Base(f), leak)
			}
		}
	}
}

func TestHookPayload_OverlaysOnlyTheNamedFields(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal(HookPayload(t, "stop.json", map[string]any{"cwd": "/x", "stop_hook_active": true}), &got); err != nil {
		t.Fatal(err)
	}

	if got["cwd"] != "/x" || got["stop_hook_active"] != true {
		t.Errorf("overlaid fields = %v/%v, want /x and true", got["cwd"], got["stop_hook_active"])
	}
	if got["session_id"] != HookSession || got["hook_event_name"] != "Stop" {
		t.Errorf("untouched fields = %v/%v, want the recording's", got["session_id"], got["hook_event_name"])
	}
}
