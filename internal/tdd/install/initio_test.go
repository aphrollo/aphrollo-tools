package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Init on a config dir with no settings.json creates one wired to the binary.
func TestInitSettings_CreatesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	changed, err := InitSettings(dir, bin, false)
	if err != nil {
		t.Fatalf("InitSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true creating settings.json")
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), `aphrollo\" gate pretooluse`) {
		t.Errorf("settings.json missing aphrollo hooks:\n%s", data)
	}
}

// Init is idempotent: the second run reports no change and rewrites nothing new.
func TestInitSettings_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := InitSettings(dir, bin, false); err != nil {
		t.Fatalf("first init: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	changed, err := InitSettings(dir, bin, false)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if changed {
		t.Error("expected changed=false on second init")
	}
	second, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(first) != string(second) {
		t.Errorf("second init rewrote the file:\n%s\n---\n%s", first, second)
	}
}

// Modifying an existing settings.json leaves a timestamped backup behind so the
// operator can recover the pre-init file.
func TestInitSettings_BacksUpExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	orig := `{"theme":"dark"}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSettings(dir, bin, false); err != nil {
		t.Fatalf("InitSettings: %v", err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "settings.json.pre-tdd-*"))
	if len(backups) == 0 {
		t.Fatal("no backup written before modifying existing settings.json")
	}
	b, _ := os.ReadFile(backups[0])
	if string(b) != orig {
		t.Errorf("backup does not hold the original: %q", b)
	}
}

// Uninstall strips the aphrollo hooks from an initialised dir.
func TestInitSettings_Uninstall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := InitSettings(dir, bin, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	changed, err := InitSettings(dir, bin, true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on uninstall")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(data), "gate pretooluse") {
		t.Errorf("uninstall left aphrollo hooks behind:\n%s", data)
	}
}

// Uninstall on a dir that was never initialised is a harmless no-op.
func TestInitSettings_UninstallMissingIsNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	changed, err := InitSettings(dir, bin, true)
	if err != nil {
		t.Fatalf("uninstall on empty dir: %v", err)
	}
	if changed {
		t.Error("expected changed=false uninstalling a missing settings.json")
	}
}
