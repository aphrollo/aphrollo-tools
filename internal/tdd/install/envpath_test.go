package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const shimDirForTest = "/home/x/.local/share/aphrollo/cargo-queue"

func TestBuildEnvPath_PrependsShimDirAndDedupes(t *testing.T) {
	t.Parallel()
	got := BuildEnvPath(shimDirForTest, []string{"/usr/bin", shimDirForTest, "/bin"}, ":")
	want := shimDirForTest + ":/usr/bin:/bin"
	if got != want {
		t.Errorf("BuildEnvPath = %q, want %q", got, want)
	}
}

func TestBuildEnvPath_WindowsSeparator(t *testing.T) {
	t.Parallel()
	shim := `C:\Users\olive\bin\cargo-queue`
	got := BuildEnvPath(shim, []string{`C:\Windows\System32`, `C:\Windows`}, ";")
	want := shim + `;C:\Windows\System32;C:\Windows`
	if got != want {
		t.Errorf("BuildEnvPath = %q, want %q", got, want)
	}
}

func envAt(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	env, _ := m["env"].(map[string]any)
	return env
}

func TestPatchSettingsEnvPath_SetsPath(t *testing.T) {
	t.Parallel()
	out, changed, err := PatchSettingsEnvPath(nil, shimDirForTest+":/usr/bin")
	if err != nil {
		t.Fatalf("PatchSettingsEnvPath: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true installing into empty settings")
	}
	env := envAt(t, out)
	if env["PATH"] != shimDirForTest+":/usr/bin" {
		t.Errorf("env.PATH = %v, want %s:/usr/bin", env["PATH"], shimDirForTest)
	}
}

// A second patch with the same pathValue is a no-op: changed=false,
// byte-identical.
func TestPatchSettingsEnvPath_Idempotent(t *testing.T) {
	t.Parallel()
	value := shimDirForTest + ":/usr/bin"
	first, _, err := PatchSettingsEnvPath(nil, value)
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	second, changed, err := PatchSettingsEnvPath(first, value)
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

// A foreign env entry (e.g. a proxy override) survives the patch, and every
// other top-level key is untouched.
func TestPatchSettingsEnvPath_PreservesForeignEnvKeyAndTopLevelKeys(t *testing.T) {
	t.Parallel()
	in := []byte(`{"env": {"DISABLE_AUTO_COMPACT": "1"}, "theme": "dark"}`)
	out, changed, err := PatchSettingsEnvPath(in, shimDirForTest)
	if err != nil {
		t.Fatalf("PatchSettingsEnvPath: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	env := envAt(t, out)
	if env["DISABLE_AUTO_COMPACT"] != "1" {
		t.Errorf("dropped foreign env key DISABLE_AUTO_COMPACT\n%s", out)
	}
	if env["PATH"] != shimDirForTest {
		t.Errorf("env.PATH = %v, want %s\n%s", env["PATH"], shimDirForTest, out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil || m["theme"] != "dark" {
		t.Errorf("dropped unrelated top-level key 'theme'\n%s", out)
	}
}

func TestStripSettingsEnvPathShim_RemovesOnlyShimDirEntry(t *testing.T) {
	t.Parallel()
	in := []byte(`{"env": {"PATH": "` + shimDirForTest + `:/usr/bin"}}`)
	out, changed, err := StripSettingsEnvPathShim(in, shimDirForTest, ":")
	if err != nil {
		t.Fatalf("StripSettingsEnvPathShim: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	env := envAt(t, out)
	if env["PATH"] != "/usr/bin" {
		t.Errorf("env.PATH = %v, want /usr/bin\n%s", env["PATH"], out)
	}
}

func TestStripSettingsEnvPathShim_RemovesPathAndEnvKeyWhenNothingLeft(t *testing.T) {
	t.Parallel()
	in := []byte(`{"env": {"PATH": "` + shimDirForTest + `"}, "theme": "dark"}`)
	out, changed, err := StripSettingsEnvPathShim(in, shimDirForTest, ":")
	if err != nil {
		t.Fatalf("StripSettingsEnvPathShim: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if _, ok := m["env"]; ok {
		t.Errorf("env key survived with nothing left in it\n%s", out)
	}
	if m["theme"] != "dark" {
		t.Errorf("dropped unrelated top-level key 'theme'\n%s", out)
	}
}

// A foreign env key beside PATH keeps the env object alive after the PATH
// key itself is removed.
func TestStripSettingsEnvPathShim_KeepsEnvKeyWhenForeignEntryRemains(t *testing.T) {
	t.Parallel()
	in := []byte(`{"env": {"PATH": "` + shimDirForTest + `", "DISABLE_AUTO_COMPACT": "1"}}`)
	out, changed, err := StripSettingsEnvPathShim(in, shimDirForTest, ":")
	if err != nil {
		t.Fatalf("StripSettingsEnvPathShim: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	env := envAt(t, out)
	if _, ok := env["PATH"]; ok {
		t.Errorf("PATH key survived removal\n%s", out)
	}
	if env["DISABLE_AUTO_COMPACT"] != "1" {
		t.Errorf("dropped foreign env key DISABLE_AUTO_COMPACT\n%s", out)
	}
}

func TestStripSettingsEnvPathShim_NoopWhenShimDirAbsent(t *testing.T) {
	t.Parallel()
	in := []byte(`{"env": {"PATH": "/usr/bin:/bin"}}`)
	out, changed, err := StripSettingsEnvPathShim(in, shimDirForTest, ":")
	if err != nil {
		t.Fatalf("StripSettingsEnvPathShim: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false when shim dir was never in PATH\n%s", out)
	}
}

func TestStripSettingsEnvPathShim_NoopWhenNoEnvKey(t *testing.T) {
	t.Parallel()
	in := []byte(`{"theme": "dark"}`)
	_, changed, err := StripSettingsEnvPathShim(in, shimDirForTest, ":")
	if err != nil {
		t.Fatalf("StripSettingsEnvPathShim: %v", err)
	}
	if changed {
		t.Error("expected changed=false with no env key at all")
	}
}

// Install on a config dir with no settings.json creates one carrying the
// shim dir first in env.PATH.
func TestInitSettingsEnvPath_CreatesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	changed, err := InitSettingsEnvPath(dir, shimDirForTest, []string{"/usr/bin"}, ":", false)
	if err != nil {
		t.Fatalf("InitSettingsEnvPath: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true creating settings.json")
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	env := envAt(t, data)
	if env["PATH"] != shimDirForTest+":/usr/bin" {
		t.Errorf("env.PATH = %v, want %s:/usr/bin", env["PATH"], shimDirForTest)
	}
}

// A second install with the same inputs is a no-op: changed=false,
// byte-identical file.
func TestInitSettingsEnvPath_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pathDirs := []string{"/usr/bin"}
	if _, err := InitSettingsEnvPath(dir, shimDirForTest, pathDirs, ":", false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	changed, err := InitSettingsEnvPath(dir, shimDirForTest, pathDirs, ":", false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Error("expected changed=false on second install")
	}
	second, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(first) != string(second) {
		t.Errorf("second install rewrote the file:\n%s\n---\n%s", first, second)
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "settings.json.pre-tdd-path-*")); len(backups) != 0 {
		t.Errorf("a no-op second install left a backup behind: %v", backups)
	}
}

// Modifying an existing settings.json leaves a timestamped backup tagged
// "-path-" behind, distinct from InitSettings' own "pre-tdd-<ts>" backup, so
// the two managed writes one `aphrollo install` run makes never collide on
// the same backup name.
func TestInitSettingsEnvPath_BacksUpExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	orig := `{"theme":"dark"}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSettingsEnvPath(dir, shimDirForTest, nil, ":", false); err != nil {
		t.Fatalf("InitSettingsEnvPath: %v", err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "settings.json.pre-tdd-path-*"))
	if len(backups) == 0 {
		t.Fatal("no backup written before modifying existing settings.json")
	}
	b, _ := os.ReadFile(backups[0])
	if string(b) != orig {
		t.Errorf("backup does not hold the original: %q", b)
	}
}

// Uninstall removes only the shim dir from env.PATH.
func TestInitSettingsEnvPath_Uninstall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := InitSettingsEnvPath(dir, shimDirForTest, []string{"/usr/bin"}, ":", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	changed, err := InitSettingsEnvPath(dir, shimDirForTest, nil, ":", true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on uninstall")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	env := envAt(t, data)
	if env["PATH"] != "/usr/bin" {
		t.Errorf("env.PATH after uninstall = %v, want /usr/bin\n%s", env["PATH"], data)
	}
}

// Uninstall on a dir that was never initialised is a harmless no-op.
func TestInitSettingsEnvPath_UninstallMissingIsNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	changed, err := InitSettingsEnvPath(dir, shimDirForTest, nil, ":", true)
	if err != nil {
		t.Fatalf("uninstall on empty dir: %v", err)
	}
	if changed {
		t.Error("expected changed=false uninstalling a missing settings.json")
	}
}
