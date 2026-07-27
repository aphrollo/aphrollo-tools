package tdd

import (
	"encoding/json"
	"strings"
	"testing"
)

// helper: parse settings JSON and return the hooks map for an event.
func hookGroups(t *testing.T, data []byte, event string) []any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	hooks, _ := m["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	return groups
}

// commandStrings returns every hook command string under an event.
func commandStrings(t *testing.T, data []byte, event string) []string {
	t.Helper()
	var out []string
	for _, g := range hookGroups(t, data, event) {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if c, ok := hm["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func hasCommandContaining(t *testing.T, data []byte, event, sub string) bool {
	for _, c := range commandStrings(t, data, event) {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

const bin = "/usr/local/bin/aphrollo"

// Installing into an empty settings file wires all session-hook events to the
// aphrollo tdd subcommands.
func TestPatchSettings_InstallsAllEvents(t *testing.T) {
	out, changed, err := PatchSettings(nil, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true installing into empty settings")
	}
	for _, want := range []struct{ event, sub string }{
		{"SessionStart", "aphrollo\" tdd sessionstart"},
		{"PreToolUse", "aphrollo\" tdd pretooluse"},
		{"PostToolUse", "aphrollo\" tdd posttooluse"},
		{"SessionEnd", "aphrollo\" tdd sessionend"},
		{"UserPromptSubmit", "aphrollo\" tdd userpromptsubmit"},
	} {
		if !hasCommandContaining(t, out, want.event, want.sub) {
			t.Errorf("%s: missing command %q\n%s", want.event, want.sub, out)
		}
	}
}

// A second patch over our own output is a no-op: changed=false, byte-identical.
func TestPatchSettings_Idempotent(t *testing.T) {
	first, _, err := PatchSettings(nil, bin)
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	second, changed, err := PatchSettings(first, bin)
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false on re-patch, got true\n%s", second)
	}
	if string(first) != string(second) {
		t.Errorf("re-patch changed bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Foreign hooks (e.g. caveman on UserPromptSubmit) survive the patch; the
// aphrollo entry is added alongside, not in place of them.
func TestPatchSettings_PreservesForeignHooks(t *testing.T) {
	in := []byte(`{
	  "hooks": {
	    "UserPromptSubmit": [
	      {"hooks": [{"type": "command", "command": "node caveman-mode-tracker.js"}]}
	    ]
	  },
	  "theme": "dark"
	}`)
	out, changed, err := PatchSettings(in, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if !hasCommandContaining(t, out, "UserPromptSubmit", "caveman-mode-tracker.js") {
		t.Errorf("dropped foreign caveman hook\n%s", out)
	}
	if !hasCommandContaining(t, out, "UserPromptSubmit", "aphrollo\" tdd userpromptsubmit") {
		t.Errorf("missing aphrollo userpromptsubmit\n%s", out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil || m["theme"] != "dark" {
		t.Errorf("dropped unrelated top-level key 'theme'\n%s", out)
	}
}

// Old claude-code-tdd Node hook entries are migrated out: after the patch the
// only TDD commands are the aphrollo ones, the node tdd-*.js entries are gone.
func TestPatchSettings_MigratesNodeHooks(t *testing.T) {
	in := []byte(`{
	  "hooks": {
	    "PreToolUse": [
	      {"matcher": "Edit|Write", "hooks": [
	        {"type": "command", "command": "/opt/node/bin/node /home/x/.claude/hooks/tdd-pre-edit.js"}
	      ]}
	    ]
	  }
	}`)
	out, _, err := PatchSettings(in, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if hasCommandContaining(t, out, "PreToolUse", "tdd-pre-edit.js") {
		t.Errorf("old node tdd hook not migrated out\n%s", out)
	}
	if !hasCommandContaining(t, out, "PreToolUse", "aphrollo\" tdd pretooluse") {
		t.Errorf("missing aphrollo pretooluse after migration\n%s", out)
	}
}

// The managed-hook marker must not assume the binary is named "aphrollo": when
// init resolves to a differently-named path (os.Executable in tests, a renamed
// install), re-patching must still recognise and replace its own entries rather
// than append duplicates.
func TestPatchSettings_IdempotentWithRenamedBinary(t *testing.T) {
	const altbin = "/opt/custom/mytool"
	first, _, err := PatchSettings(nil, altbin)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, changed, err := PatchSettings(first, altbin)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if changed {
		t.Errorf("re-patch with renamed binary changed the file (duplicated hooks)\n%s", second)
	}
	var n int
	for _, c := range commandStrings(t, second, "PreToolUse") {
		if strings.Contains(c, "tdd pretooluse") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expected exactly 1 pretooluse hook, got %d\n%s", n, second)
	}
}

// Uninstall must also recognise a renamed binary's entries.
func TestStripSettings_RenamedBinary(t *testing.T) {
	installed, _, err := PatchSettings(nil, "/opt/custom/mytool")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	out, changed, err := StripSettings(installed)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true stripping renamed-binary hooks")
	}
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "SessionEnd", "UserPromptSubmit"} {
		if hasCommandContaining(t, out, ev, "tdd ") {
			t.Errorf("%s: renamed-binary tdd entry survived strip\n%s", ev, out)
		}
	}
}

// Uninstall strips every aphrollo tdd entry but leaves foreign hooks intact.
func TestStripSettings_RemovesOnlyManaged(t *testing.T) {
	installed, _, err := PatchSettings([]byte(`{
	  "hooks": {"UserPromptSubmit": [
	    {"hooks": [{"type": "command", "command": "node caveman-mode-tracker.js"}]}
	  ]}
	}`), bin)
	if err != nil {
		t.Fatalf("setup patch: %v", err)
	}
	out, changed, err := StripSettings(installed)
	if err != nil {
		t.Fatalf("StripSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true stripping installed settings")
	}
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "SessionEnd", "UserPromptSubmit"} {
		if hasCommandContaining(t, out, ev, "aphrollo tdd") {
			t.Errorf("%s: aphrollo entry survived strip\n%s", ev, out)
		}
	}
	if !hasCommandContaining(t, out, "UserPromptSubmit", "caveman-mode-tracker.js") {
		t.Errorf("strip removed foreign caveman hook\n%s", out)
	}
}
