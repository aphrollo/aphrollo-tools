package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// statusLineCommand reads settings.json's statusLine command, "" when absent.
func statusLineCommand(t *testing.T, doc []byte) string {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	sl, ok := root["statusLine"].(map[string]any)
	if !ok {
		return ""
	}
	cmd, _ := sl["command"].(string)
	return cmd
}

// TestPatchSettings_WiresTheStatusLineAtTheBinary pins that init owns the
// statusline the same way it owns the hooks: pointed at the binary that wrote
// it, so the badge and the gate can never be different builds.
func TestPatchSettings_WiresTheStatusLineAtTheBinary(t *testing.T) {
	out, changed, err := PatchSettings(nil, `C:\bin\aphrollo.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a fresh settings.json must be changed")
	}
	want := `"C:/bin/aphrollo.exe" gate statusline`
	if got := statusLineCommand(t, out); got != want {
		t.Fatalf("statusLine command = %q, want %q", got, want)
	}
	// Idempotent: a second patch over its own output changes nothing.
	_, changed2, err := PatchSettings(out, `C:\bin\aphrollo.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Fatal("a second patch must be a no-op")
	}
}

// TestPatchSettings_ReplacesARetiredStatusLineScript names the two scripts
// aphrollo supersedes. Leaving one in place means the badge reports a gate
// that no longer exists.
func TestPatchSettings_ReplacesARetiredStatusLineScript(t *testing.T) {
	for _, script := range []string{"caveman-statusline.sh", "tdd-statusline.sh"} {
		existing := []byte(`{"statusLine":{"type":"command","command":"bash ~/.claude/hooks/` + script + `","padding":0}}`)
		out, _, err := PatchSettings(existing, "/bin/aphrollo")
		if err != nil {
			t.Fatal(err)
		}
		want := `"/bin/aphrollo" gate statusline`
		if got := statusLineCommand(t, out); got != want {
			t.Errorf("with %s installed, statusLine = %q, want %q", script, got, want)
		}
		if !strings.Contains(string(out), `"padding"`) {
			t.Errorf("with %s installed, the surrounding statusLine settings were dropped:\n%s", script, out)
		}
	}
}

// TestPatchSettings_LeavesAForeignStatusLineAlone is the limit of that
// ownership: a statusline this tool never wrote, and did not supersede, is the
// user's — overwriting it would be init deleting a setting nobody asked it to.
func TestPatchSettings_LeavesAForeignStatusLineAlone(t *testing.T) {
	existing := []byte(`{"statusLine":{"type":"command","command":"my-own-prompt --fancy"}}`)
	out, _, err := PatchSettings(existing, "/bin/aphrollo")
	if err != nil {
		t.Fatal(err)
	}
	if got := statusLineCommand(t, out); got != "my-own-prompt --fancy" {
		t.Fatalf("statusLine = %q, want the user's own command untouched", got)
	}
}

// TestStripSettings_RemovesOnlyTheManagedStatusLine keeps uninstall honest:
// it takes back what it wrote and nothing else.
func TestStripSettings_RemovesOnlyTheManagedStatusLine(t *testing.T) {
	managed, _, err := PatchSettings(nil, "/bin/aphrollo")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := StripSettings(managed)
	if err != nil {
		t.Fatal(err)
	}
	if got := statusLineCommand(t, out); got != "" {
		t.Fatalf("statusLine survived uninstall: %q", got)
	}

	foreign := []byte(`{"statusLine":{"type":"command","command":"my-own-prompt"}}`)
	out, _, err = StripSettings(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if got := statusLineCommand(t, out); got != "my-own-prompt" {
		t.Fatalf("uninstall removed a foreign statusLine: %q", got)
	}
}

// TestPruneRetiredHooks_DeletesTheNodePluginLeftovers clears the hook scripts
// the binary replaced. They are not inert: settings.json entries pointing at
// them double-fired every event, and a leftover statusline script reports on a
// gate that is not installed.
func TestPruneRetiredHooks_DeletesTheNodePluginLeftovers(t *testing.T) {
	cfg := t.TempDir()
	hooks := filepath.Join(cfg, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	retired := []string{
		"tdd-post-edit.js", "tdd-pre-edit.sh", "tamper.js",
		"tdd-cargomutants.test.js", "tdd-winspawn.test.js", "caveman-statusline.sh",
	}
	keep := []string{"my-own-hook.sh", "notify.js", "tdd-notes.md"}
	for _, name := range append(append([]string{}, retired...), keep...) {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := PruneRetiredHooks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(retired)
	slices.Sort(removed)
	if !slices.Equal(removed, retired) {
		t.Fatalf("removed = %v, want %v", removed, retired)
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(hooks, name)); err != nil {
			t.Errorf("%s must be left alone: %v", name, err)
		}
	}
	again, err := PruneRetiredHooks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second run reported %v, want nothing", again)
	}
}
