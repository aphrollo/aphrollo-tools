package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `tdd init` (no --no-git) also installs the git gate: shims plus a global
// core.hooksPath. One command sets up everything.
func TestRun_TDDInit_GitGate(t *testing.T) {
	t.Chdir(t.TempDir())
	isolateGit(t)
	// This test's own --git-hooks-dir sits under t.TempDir() beside the
	// isolated global config above — the sanctioned dogfooding shape
	// installGitGate's temp/scratchpad refusal exists to let through.
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	// An explicit --cargo-shim-dir, same reasoning as --git-hooks-dir:
	// cargo-shim install (task A7) derives its output dir from --bin by
	// default, and the stand-in binary below lives under a t.TempDir()
	// whose queue dir this test has no reason to keep.
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--cargo-shim-dir", shimDir, "--bin", fakeInstalledBin(t)},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); err != nil {
		t.Errorf("pre-commit shim not installed: %v", err)
	}
	hp, _ := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	if strings.TrimSpace(string(hp)) != hooks {
		t.Errorf("core.hooksPath = %q, want %q", strings.TrimSpace(string(hp)), hooks)
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--uninstall"},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("uninstall exit = %d: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); !os.IsNotExist(err) {
		t.Error("pre-commit shim survived uninstall")
	}
	hp, _ = exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	if strings.TrimSpace(string(hp)) != "" {
		t.Errorf("core.hooksPath still set after uninstall: %q", strings.TrimSpace(string(hp)))
	}
}

// `tdd init` also installs the cargo-queue shim (task A7) next to --bin:
// a session can prepend that dir to its OWN PATH so a direct `cargo`
// invocation queues behind the same machine-wide build lock the hooks/gates
// use. --uninstall deliberately leaves it in place (see InstallCargoShim's
// doc comment).
func TestRun_TDDInit_CargoShim(t *testing.T) {
	t.Chdir(t.TempDir())
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1") // see TestRun_TDDInit_GitGate
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")
	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A batch shim left over from the previous install, which init must
	// delete: cmd.exe strips `^` from an argument and re-splits quoted ones,
	// so it is not a slow path, it is a wrong answer.
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(shimDir, "cargo.cmd")
	if err := os.WriteFile(stale, []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--cargo-shim-dir", shimDir, "--bin", bin},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstderr: %s", code, errb.String())
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("cargo.cmd survived init: %v", err)
	}
	if !strings.Contains(out.String(), "removed the retired batch shim") {
		t.Errorf("init must report the batch-shim removal, got:\n%s", out.String())
	}
	shData, err := os.ReadFile(filepath.Join(shimDir, "cargo"))
	if err != nil {
		t.Fatalf("cargo (sh) not written: %v", err)
	}
	if !strings.Contains(string(shData), filepath.ToSlash(bin)) {
		t.Errorf("cargo (sh) missing the resolved bin path:\n%s", shData)
	}
	if runtime.GOOS == "windows" {
		exe, err := os.ReadFile(filepath.Join(shimDir, "cargo.exe"))
		if err != nil {
			t.Fatalf("cargo.exe shim not written: %v", err)
		}
		if string(exe) != "APHROLLO" {
			t.Errorf("cargo.exe = %q, want a copy of the binary", exe)
		}
	}

	// --uninstall must NOT remove the cargo-shim files.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--cargo-shim-dir", shimDir, "--uninstall"},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("uninstall exit = %d: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(shimDir, "cargo")); err != nil {
		t.Errorf("cargo-shim files must survive --uninstall, got: %v", err)
	}
}
