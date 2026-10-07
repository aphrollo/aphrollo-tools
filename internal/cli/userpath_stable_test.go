package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// A user-space binary is reached through the root's launcher, which follows
// `current`; so the user PATH and the agent's env.PATH lead with the shim dir
// and the root, never a version directory, and the version directories an
// earlier install put there are dropped. A version switch then reaches every
// open shell without touching PATH.
func TestInstall_AUserSpaceBinLeadsPathWithTheStableDirsAndDropsVersionedOnes(t *testing.T) {
	userspaceHome(t)
	d := newInstallDirs(t)
	root, err := userbin.Root()
	if err != nil {
		t.Fatal(err)
	}
	bin := userbin.BinaryPath(root, "7.1.0")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	d.bin = writeFakeBin(t, bin)
	d.shim = filepath.Join(filepath.Dir(root), "cargo-queue")
	stale, staleShim := filepath.Join(root, "7.0.0"), filepath.Join(root, "7.0.0", "cargo-queue")
	f := &fakeUserPath{raw: staleShim + ";" + stale + `;C:\Tools`}
	useFakeUserPath(t, f)
	sep := pathListSep()
	seed := `{"env":{"PATH":` + strconvQuote(staleShim+sep+"/opt/tools"+sep+stale) + `}}`
	if err := os.WriteFile(filepath.Join(d.cfg, "settings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	d.install(t)

	if want := d.shim + ";" + root + `;C:\Tools`; f.raw != want {
		t.Errorf("user PATH = %q, want %q", f.raw, want)
	}
	env := settingsEnvPath(t, d.cfg)
	if len(env) < 3 || env[0] != d.shim || env[1] != root {
		t.Errorf("env.PATH starts %q, want the shim dir then the root", env[:min(len(env), 3)])
	}
	for _, gone := range []string{stale, staleShim} {
		if slices.Contains(env, gone) {
			t.Errorf("env.PATH still holds the version directory %s", gone)
		}
	}
	if !slices.Contains(env, "/opt/tools") {
		t.Error("env.PATH lost a foreign entry")
	}
}

// Doctor judges the user PATH by the same two directories install writes.
func TestDoctorInput_AUserSpaceBinIsJudgedByItsLauncherDir(t *testing.T) {
	userspaceHome(t)
	root, err := userbin.Root()
	if err != nil {
		t.Fatal(err)
	}
	bin := userbin.BinaryPath(root, "7.1.0")
	orig := execPathFn
	execPathFn = func() (string, error) { return bin, nil }
	t.Cleanup(func() { execPathFn = orig })
	if got := doctorInput("", "", ".").LauncherDir; got != root {
		t.Errorf("LauncherDir = %q, want the root %q", got, root)
	}
}

func settingsEnvPath(t *testing.T, cfg string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return strings.Split(s.Env["PATH"], pathListSep())
}

func strconvQuote(s string) string {
	var b bytes.Buffer
	_ = json.NewEncoder(&b).Encode(s)
	return strings.TrimSpace(b.String())
}
