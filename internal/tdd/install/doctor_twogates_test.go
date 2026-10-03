package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// addTrellisToSettings wires a trellis hook and plugin beside the managed
// aphrollo hooks the healthy install already wrote.
func addTrellisToSettings(t *testing.T, cfg string, edit func(root map[string]any)) {
	t.Helper()
	path := filepath.Join(cfg, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	edit(root)
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctor_NamesTwoLiveGatesWhenATrellisPluginIsEnabledBesideAphrolloHooks(t *testing.T) {
	in := healthyInstall(t)
	if c := check(t, Doctor(in), "one live gate"); !c.OK {
		t.Fatalf("control: aphrollo hooks alone are one gate, got %q", c.Detail)
	}
	addTrellisToSettings(t, in.ConfigDir, func(root map[string]any) {
		root["enabledPlugins"] = map[string]any{"trellis@aphrollo": true}
	})

	c := check(t, Doctor(in), "one live gate")
	if c.OK {
		t.Fatal("aphrollo hooks plus an enabled trellis plugin must be a finding")
	}
	for _, want := range []string{"aphrollo", "trellis", "aphrollo gate init --uninstall", "disable the trellis plugin"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", c.Detail, want)
		}
	}
}

func TestDoctor_ADisabledTrellisPluginIsNotASecondGate(t *testing.T) {
	in := healthyInstall(t)
	addTrellisToSettings(t, in.ConfigDir, func(root map[string]any) {
		root["enabledPlugins"] = map[string]any{"trellis@aphrollo": false}
	})
	if c := check(t, Doctor(in), "one live gate"); !c.OK {
		t.Fatalf("a disabled plugin gates nothing, got %q", c.Detail)
	}
}

func TestDoctor_NamesTwoLiveGatesWhenATrellisHookIsWiredDirectlyIntoSettings(t *testing.T) {
	in := healthyInstall(t)
	addTrellisToSettings(t, in.ConfigDir, func(root map[string]any) {
		hooks := root["hooks"].(map[string]any)
		hooks["PreToolUse"] = append(toGroups(hooks["PreToolUse"]), map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": `"C:/tools/trellis.exe" hook pretooluse`}},
		})
	})
	if c := check(t, Doctor(in), "one live gate"); c.OK {
		t.Fatal("a trellis hook beside the aphrollo hooks must be a finding")
	}
}
