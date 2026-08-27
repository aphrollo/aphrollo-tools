package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallCargoShim_WritesBothFiles pins task A7's install contract: a
// dir named cargo-queue holding a Windows cargo.cmd and an extensionless
// POSIX cargo script, both invoking `<bin> tdd cargo` — a session opts into
// the queue by prepending this dir to its OWN PATH (never touched here).
func TestInstallCargoShim_WritesBothFiles(t *testing.T) {
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")
	bin := `C:\Users\olive\bin\aphrollo.exe`

	changed, err := InstallCargoShim(shimDir, bin)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on a fresh install")
	}

	cmdData, err := os.ReadFile(filepath.Join(shimDir, "cargo.cmd"))
	if err != nil {
		t.Fatalf("cargo.cmd not written: %v", err)
	}
	if !strings.Contains(string(cmdData), bin) || !strings.Contains(string(cmdData), "tdd cargo %*") {
		t.Fatalf("cargo.cmd wrong content:\n%s", cmdData)
	}

	shData, err := os.ReadFile(filepath.Join(shimDir, "cargo"))
	if err != nil {
		t.Fatalf("cargo (sh) not written: %v", err)
	}
	if !strings.Contains(string(shData), "tdd cargo \"$@\"") {
		t.Fatalf("cargo (sh) wrong content:\n%s", shData)
	}
	if !strings.HasPrefix(string(shData), "#!/bin/sh\n") {
		t.Fatalf("cargo (sh) must start with a shebang:\n%s", shData)
	}
}

// TestInstallCargoShim_IdempotentSecondCall guards re-running install
// (`aphrollo tdd init` run twice): identical content must report
// changed=false the second time, matching every other managed-shim writer
// in this package.
func TestInstallCargoShim_IdempotentSecondCall(t *testing.T) {
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")
	bin := `C:\Users\olive\bin\aphrollo.exe`

	if _, err := InstallCargoShim(shimDir, bin); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallCargoShim(shimDir, bin)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("a second identical install must report changed=false")
	}
}

// TestInstallCargoShim_UpdatesWhenBinPathChanges guards a re-point (e.g. the
// aphrollo binary renamed) actually rewriting the shim content, not silently
// keeping the stale path forever.
func TestInstallCargoShim_UpdatesWhenBinPathChanges(t *testing.T) {
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")
	binOld := `C:\Users\olive\bin\aphrollo.exe`
	if _, err := InstallCargoShim(shimDir, binOld); err != nil {
		t.Fatal(err)
	}

	binNew := `C:\Users\olive\bin\aphrollo-renamed.exe`
	changed, err := InstallCargoShim(shimDir, binNew)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a changed bin path must report changed=true")
	}
	cmdData, err := os.ReadFile(filepath.Join(shimDir, "cargo.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cmdData), binNew) {
		t.Fatalf("cargo.cmd must reflect the NEW bin path, got:\n%s", cmdData)
	}
}
