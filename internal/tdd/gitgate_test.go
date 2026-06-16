package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGitConfig points git's global + system config at temp/empty files so
// the test never reads or writes the real ~/.gitconfig.
func isolateGitConfig(t *testing.T) string {
	t.Helper()
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
	for name, sub := range map[string]string{"pre-commit": "precommit", "pre-push": "prepush"} {
		data, err := os.ReadFile(filepath.Join(hooksDir, name))
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		if !strings.Contains(string(data), "tdd "+sub) {
			t.Errorf("%s does not invoke tdd %s:\n%s", name, sub, data)
		}
		if fi, _ := os.Stat(filepath.Join(hooksDir, name)); fi != nil && fi.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable", name)
		}
	}
	if got := globalHooksPath(t); got != hooksDir {
		t.Errorf("core.hooksPath = %q, want %q", got, hooksDir)
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
