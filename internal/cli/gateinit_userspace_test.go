package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// userspaceHome gives the test its own account home, so no user-space install
// of another test is seen.
func userspaceHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
}

// `aphrollo update` runs `gate init --bin <the new user-space binary>`. A
// versioned directory is pruned by a later update, so hooks written for it must
// not keep it as their installed path: they run the user-space current and
// fall back to the path the update replaced.
func TestGateInit_AUserSpaceBinKeepsTheNamedInstalledPathAsTheFallback(t *testing.T) {
	userspaceHome(t)
	f := newGateInitFixture(t)
	root, err := userbin.Root()
	if err != nil {
		t.Fatal(err)
	}
	bin := userbin.BinaryPath(root, "7.0.0")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeBin(t, bin)
	old := fakeInstalledBin(t)

	code, _, errb := f.run(bin, "--fallback-bin", old)
	if code != 0 {
		t.Fatalf("init exit = %d\nstderr: %s", code, errb)
	}
	want := "aphrollo_fallback='" + old + "'"
	for _, file := range []string{filepath.Join(f.hooks, "pre-commit"), filepath.Join(f.shimDir, "git"), filepath.Join(f.cfg, "settings.json")} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not carry %s", file, want)
		}
		if strings.Contains(string(data), "7.0.0") {
			t.Errorf("%s names the versioned directory, which the next update prunes", file)
		}
	}
}

// A second init without the flag must not inherit the first one's fallback.
func TestGateInit_TheFallbackFlagDoesNotLeakIntoTheNextInit(t *testing.T) {
	userspaceHome(t)
	f := newGateInitFixture(t)
	if code, _, errb := f.run(fakeInstalledBin(t), "--fallback-bin", "/somewhere/else"); code != 0 {
		t.Fatalf("first init exit = %d: %s", code, errb)
	}
	bin := fakeInstalledBin(t)
	if code, _, errb := f.run(bin); code != 0 {
		t.Fatalf("second init exit = %d: %s", code, errb)
	}
	data, err := os.ReadFile(filepath.Join(f.hooks, "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/somewhere/else") || !strings.Contains(string(data), "aphrollo_fallback='"+bin+"'") {
		t.Fatalf("the second init's shim does not name its own --bin:\n%s", data)
	}
}

// gate init (what `aphrollo install` runs) leaves the launcher beside the
// versions, falling back to the path it was given.
func TestGateInit_WritesTheLauncherWithTheInstalledPathAsFallback(t *testing.T) {
	userspaceHome(t)
	f := newGateInitFixture(t)
	root, _ := userbin.Root()
	bin := fakeInstalledBin(t)
	if code, _, errb := f.run(bin); code != 0 {
		t.Fatalf("init exit = %d: %s", code, errb)
	}
	data, err := os.ReadFile(userbin.LauncherPath(root))
	if err != nil || !strings.Contains(strings.ReplaceAll(string(data), `\`, "/"), bin) {
		t.Fatalf("launcher = %q, %v; want it to name %s", data, err, bin)
	}
}
