package tddtest

import (
	"encoding/json"
	"testing"
)

// BashPayload is a Bash PreToolUse payload with a fixed tool_use_id.
func BashPayload(t *testing.T, session, cwd, command string) []byte {
	t.Helper()
	return BashPayloadID(t, session, "toolu_single", cwd, command)
}

// BashPayloadID is a Bash PreToolUse payload for one tool call.
func BashPayloadID(t *testing.T, session, toolUseID, cwd, command string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id":  session,
		"tool_use_id": toolUseID,
		"cwd":         cwd,
		"tool_name":   "Bash",
		"tool_input":  map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// PowerShellPayload is BashPayload's PowerShell twin, same tool_input shape
// under a different tool_name: the PowerShell tool is classified exactly
// like Bash (issue #118).
func PowerShellPayload(t *testing.T, session, cwd, command string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id": session,
		"cwd":        cwd,
		"tool_name":  "PowerShell",
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// PreEditJSON builds a PreToolUse payload naming a single edited file.
func PreEditJSON(t *testing.T, tool, filePath, session string) []byte {
	t.Helper()
	payload := map[string]any{
		"tool_name":  tool,
		"session_id": session,
		"tool_input": map[string]any{"file_path": filePath},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// PostPayload builds a PostToolUse payload for a Go project at root.
func PostPayload(tool, file string) []byte {
	in := map[string]any{
		"session_id": "sess-post",
		"tool_name":  tool,
		"tool_input": map[string]any{"file_path": file},
	}
	b, _ := json.Marshal(in)
	return b
}

// RatchetPayload is an edit payload for path carrying extra tool_input fields.
func RatchetPayload(t *testing.T, tool, path string, fields map[string]any) []byte {
	t.Helper()
	input := map[string]any{"file_path": path}
	for k, v := range fields {
		input[k] = v
	}
	raw, err := json.Marshal(map[string]any{"tool_name": tool, "tool_input": input})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ArgvValueOf is the value of one flag in a rendered command line.
func ArgvValueOf(t *testing.T, argv []string, flag string) string {
	t.Helper()
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("argv carries no %s: %v", flag, argv)
	return ""
}
