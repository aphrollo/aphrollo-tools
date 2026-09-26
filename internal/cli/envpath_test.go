package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// envAt reads settings.json under cfg and returns its env.PATH value, "" when
// there is no env key or no PATH inside it.
func envPathAt(t *testing.T, cfg string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	env, _ := m["env"].(map[string]any)
	v, _ := env["PATH"].(string)
	return v
}

// `aphrollo install` writes the queue shim dir first into settings.json's
// env.PATH — the only PATH the agent's own Bash tool resolves through, since
// it reads neither ~/.bashrc nor ~/.profile (issue #911).
func TestRun_Install_WritesAgentEnvPathWithShimDirFirst(t *testing.T) {
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := t.TempDir()
	gitInitRepo(t, repo)
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	binDir := t.TempDir()
	bin := writeFakeBin(t, filepath.Join(binDir, "aphrollo.exe"))
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")

	var out, errb bytes.Buffer
	code := Run([]string{"install", "--repo", repo, "--config-dir", cfg, "--git-hooks-dir", hooks,
		"--bin", bin, "--cargo-shim-dir", shimDir},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}

	value := envPathAt(t, cfg)
	sep := pathListSep()
	first := strings.SplitN(value, sep, 2)[0]
	if first != shimDir {
		t.Fatalf("env.PATH does not start with the shim dir: %q\nfull value: %q", first, value)
	}
	if !strings.Contains(out.String(), "added the queue shim to the agent's env.PATH") {
		t.Errorf("install did not report writing env.PATH:\n%s", out.String())
	}
}

// A second install with nothing changed is a no-op on settings.json: the
// file is byte-identical and the report says so.
func TestRun_Install_EnvPathIdempotentOnRerun(t *testing.T) {
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := t.TempDir()
	gitInitRepo(t, repo)
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	binDir := t.TempDir()
	bin := writeFakeBin(t, filepath.Join(binDir, "aphrollo.exe"))
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")

	run := func() (string, string) {
		var out, errb bytes.Buffer
		code := Run([]string{"install", "--repo", repo, "--config-dir", cfg, "--git-hooks-dir", hooks,
			"--bin", bin, "--cargo-shim-dir", shimDir},
			strings.NewReader(""), &out, &errb)
		if code != 0 {
			t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
		}
		return out.String(), errb.String()
	}
	run()
	first, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := run()
	second, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("second install rewrote settings.json:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.Contains(out2, "agent env.PATH already up to date") {
		t.Errorf("second install did not report env.PATH already up to date:\n%s", out2)
	}
}

// --uninstall removes only the shim dir from env.PATH — every other entry
// (the box's own PATH at install time) survives.
func TestRun_Install_Uninstall_RemovesOnlyShimDirFromEnvPath(t *testing.T) {
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := t.TempDir()
	gitInitRepo(t, repo)
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	binDir := t.TempDir()
	bin := writeFakeBin(t, filepath.Join(binDir, "aphrollo.exe"))
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")

	var out, errb bytes.Buffer
	code := Run([]string{"install", "--repo", repo, "--config-dir", cfg, "--git-hooks-dir", hooks,
		"--bin", bin, "--cargo-shim-dir", shimDir},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	before := envPathAt(t, cfg)
	if !strings.Contains(before, shimDir) {
		t.Fatalf("setup: shim dir missing from env.PATH before uninstall: %q", before)
	}

	out.Reset()
	errb.Reset()
	code = Run([]string{"install", "--uninstall", "--repo", repo, "--config-dir", cfg,
		"--git-hooks-dir", hooks, "--bin", bin, "--cargo-shim-dir", shimDir},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("uninstall exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}

	after := envPathAt(t, cfg)
	if strings.Contains(after, shimDir) {
		t.Fatalf("shim dir survived uninstall in env.PATH: %q", after)
	}
	sep := pathListSep()
	beforeDirs := strings.Split(before, sep)
	afterDirs := strings.Split(after, sep)
	if len(afterDirs) != len(beforeDirs)-1 {
		t.Fatalf("uninstall changed more than the shim dir entry: before=%q after=%q", before, after)
	}
	if !strings.Contains(out.String(), "removed the queue shim from the agent's env.PATH") {
		t.Errorf("uninstall did not report removing env.PATH entry:\n%s", out.String())
	}
}
