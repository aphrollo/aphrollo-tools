package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolateGitConfig points git's global + system config at temp/empty files so
// the test never reads or writes the real ~/.gitconfig.
func isolateGitConfig(t *testing.T) string {
	t.Helper()
	// Drop the repo-pointing GIT_* vars a git hook exports (GIT_DIR,
	// GIT_INDEX_FILE, …). Under the aphrollo tdd pre-commit gate they point at
	// the REAL repo; without this, fixture git ops would target (and can
	// corrupt) the real .git. Restored on cleanup.
	for _, k := range []string{
		"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE",
		"GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX",
	} {
		if v, ok := os.LookupEnv(k); ok {
			os.Unsetenv(k)
			t.Cleanup(func() { os.Setenv(k, v) })
		}
	}
	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	return gc
}

func globalHooksPath(t *testing.T) string {
	t.Helper()
	out, _ := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	return strings.TrimSpace(string(out))
}

// Installing the git gate writes the two managed shims and points git's global
// core.hooksPath at the hooks dir, so every repo is gated by one command.
func TestInitGitGate_Installs(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")

	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true installing the git gate")
	}
	for name, sub := range map[string]string{"pre-commit": "precommit"} {
		data, err := os.ReadFile(filepath.Join(hooksDir, name))
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		if !strings.Contains(string(data), "tdd "+sub) {
			t.Errorf("%s does not invoke tdd %s:\n%s", name, sub, data)
		}
		// NTFS carries no exec bit (os.Stat reports 0666); git runs the shim
		// through sh on Windows regardless, so the bit only matters elsewhere.
		if fi, _ := os.Stat(filepath.Join(hooksDir, name)); runtime.GOOS != "windows" && fi != nil && fi.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable", name)
		}
	}
	// The gate is mechanical-only now: pre-push is no longer a managed hook, so
	// install must NOT write a pre-push shim.
	if _, err := os.Stat(filepath.Join(hooksDir, "pre-push")); !os.IsNotExist(err) {
		t.Errorf("pre-push shim should not be installed (gate is mechanical-only), stat err=%v", err)
	}
	if got := globalHooksPath(t); got != hooksDir {
		t.Errorf("core.hooksPath = %q, want %q", got, hooksDir)
	}
}

// A box installed before the mechanical-only change has a MANAGED pre-push shim
// in the hooks dir. The next install must prune that stranded managed shim so
// the lingering pre-push hook stops firing — while never touching a foreign
// (hand-written) pre-push hook.
func TestInitGitGate_PrunesStrandedManagedPrePush(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate a previously-installed managed pre-push shim.
	managed := "#!/bin/sh\n" + installMarker + "\nexec /usr/local/bin/aphrollo tdd prepush \"$@\"\n"
	prePush := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(prePush, []byte(managed), 0o755); err != nil {
		t.Fatal(err)
	}

	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	if !changed {
		t.Error("expected changed=true (pruned the stranded pre-push shim)")
	}
	if _, err := os.Stat(prePush); !os.IsNotExist(err) {
		t.Errorf("stranded managed pre-push shim was not pruned, stat err=%v", err)
	}
}

// A FOREIGN (hand-written) pre-push hook must survive install: the prune only
// removes shims this tool wrote, never a user's own hook.
func TestInitGitGate_PreservesForeignPrePushOnPrune(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho my own pre-push\n"
	prePush := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(prePush, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	data, _ := os.ReadFile(prePush)
	if string(data) != foreign {
		t.Errorf("foreign pre-push was clobbered:\n%s", data)
	}
}

// The git gate install is idempotent.
func TestInitGitGate_Idempotent(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("first: %v", err)
	}
	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if changed {
		t.Error("expected changed=false on re-install")
	}
}

// A foreign (non-managed) hook is never overwritten.
func TestInitGitGate_PreservesForeignHook(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho mine\n"
	pc := filepath.Join(hooksDir, "pre-commit")
	if err := os.WriteFile(pc, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	data, _ := os.ReadFile(pc)
	if string(data) != foreign {
		t.Errorf("foreign pre-commit was overwritten:\n%s", data)
	}
}

// A pre-existing FOREIGN global core.hooksPath must not be silently clobbered:
// the user has their own global hooks. Install refuses with an error and leaves
// the existing value intact, rather than overwriting a path it could never
// restore on uninstall.
func TestInitGitGate_RefusesForeignHooksPath(t *testing.T) {
	isolateGitConfig(t)
	foreignPath := filepath.Join(t.TempDir(), "their-hooks")
	if out, err := exec.Command("git", "config", "--global", "core.hooksPath", foreignPath).CombinedOutput(); err != nil {
		t.Fatalf("seed core.hooksPath: %v: %s", err, out)
	}

	hooksDir := filepath.Join(t.TempDir(), "hooks")
	_, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err == nil {
		t.Fatal("InitGitGate: want error refusing to clobber a foreign core.hooksPath, got nil")
	}
	if got := globalHooksPath(t); got != foreignPath {
		t.Errorf("foreign core.hooksPath was changed to %q, want preserved %q", got, foreignPath)
	}
}

// Uninstall removes the managed shims and unsets core.hooksPath when it points
// at our dir.
func TestInitGitGate_Uninstall(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on uninstall")
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "pre-commit")); !os.IsNotExist(err) {
		t.Error("pre-commit shim survived uninstall")
	}
	if got := globalHooksPath(t); got != "" {
		t.Errorf("core.hooksPath still set to %q after uninstall", got)
	}
}
