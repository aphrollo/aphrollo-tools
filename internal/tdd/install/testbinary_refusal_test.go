package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// A hook, shim or settings entry whose command is a Go test binary makes every
// later git call or hook on the box exec that binary with gate arguments, and a
// test binary answers by running its whole suite (#997). No writer may emit one,
// whatever directory it was pointed at.
const goTestBinary = "/tmp/go-build123/b001/tdd.test"

func assertNothingWritten(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused install still wrote %d entries into %s", len(entries), dir)
	}
}

func TestInitSettings_RefusesATestBinaryAsTheHookCommand(t *testing.T) {
	dir := t.TempDir()
	changed, err := InitSettings(dir, goTestBinary, false)
	if !errors.Is(err, proc.ErrTestBinary) || changed {
		t.Fatalf("InitSettings(test binary) = %v, %v; want ErrTestBinary and no change", changed, err)
	}
	assertNothingWritten(t, dir)
}

func TestInitSettings_UninstallNeedsNoBinaryAndStillWorksFromATestBinary(t *testing.T) {
	dir := t.TempDir()
	if _, err := InitSettings(dir, "/opt/aphrollo/bin/aphrollo", false); err != nil {
		t.Fatal(err)
	}
	changed, err := InitSettings(dir, goTestBinary, true)
	if err != nil || !changed {
		t.Fatalf("uninstall from a test binary = %v, %v; want it to remove the hooks", changed, err)
	}
}

func TestInstallCargoShim_RefusesATestBinary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "q")
	if _, err := InstallCargoShim(dir, goTestBinary); !errors.Is(err, proc.ErrTestBinary) {
		t.Fatalf("InstallCargoShim(test binary) = %v, want ErrTestBinary", err)
	}
	assertNothingWritten(t, dir)
}

func TestInstallGitShim_RefusesATestBinary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "q")
	if _, err := InstallGitShim(dir, goTestBinary); !errors.Is(err, proc.ErrTestBinary) {
		t.Fatalf("InstallGitShim(test binary) = %v, want ErrTestBinary", err)
	}
	assertNothingWritten(t, dir)
}

func TestInstallShimExes_RefusesATestBinaryAsTheSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "q")
	if _, err := InstallShimExes(dir, goTestBinary); !errors.Is(err, proc.ErrTestBinary) {
		t.Fatalf("InstallShimExes(test binary) = %v, want ErrTestBinary", err)
	}
	assertNothingWritten(t, dir)
}

func TestInstallGitGate_RefusesATestBinaryBeforeTouchingTheHooksDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	t.Setenv(HooksDirUnsafeEnv, "1")
	if _, err := installGitGate(dir, goTestBinary); !errors.Is(err, proc.ErrTestBinary) {
		t.Fatalf("installGitGate(test binary) = %v, want ErrTestBinary", err)
	}
	assertNothingWritten(t, dir)
}

func TestBuildInstallPlan_RefusesATestBinary(t *testing.T) {
	root := makeGoRepo(t)
	if _, err := BuildInstallPlan(root, goTestBinary); !errors.Is(err, proc.ErrTestBinary) {
		t.Fatalf("BuildInstallPlan(test binary) = %v, want ErrTestBinary", err)
	}
}
