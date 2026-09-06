package refactor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRename_RustAnalyzer validates the rust-analyzer path end-to-end.
func TestRename_RustAnalyzer(t *testing.T) {
	if _, err := exec.LookPath("rust-analyzer"); err != nil {
		t.Skip("rust-analyzer not on PATH; skipping e2e")
	}
	// On PATH is not the same as usable. GitHub's Windows runner ships a
	// rust-analyzer that resolves but dies during LSP initialize with a bare
	// EOF, which surfaced as a Rename failure indistinguishable from a real
	// regression. Probe it once: a binary that cannot report its own version
	// cannot serve a rename either, and that is an environment fact rather
	// than something this test can assert about.
	probe, cancelProbe := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelProbe()
	if err := exec.CommandContext(probe, "rust-analyzer", "--version").Run(); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — this test still asserts for real once rust-analyzer actually runs.
		t.Skipf("rust-analyzer on PATH but not runnable (%v); skipping e2e", err)
	}

	dir := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"),
		[]byte("[package]\nname = \"m\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(srcDir, "lib.rs")
	if err := os.WriteFile(lib,
		[]byte("pub fn greet() -> &'static str {\n    \"hi\"\n}\n\npub fn caller() -> &'static str {\n    greet()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := Rename(ctx, RenameRequest{File: lib, Line: 1, Symbol: "greet", NewName: "hello"})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	combined := ""
	for _, f := range res.Files {
		combined += f.Diff
	}
	if !strings.Contains(combined, "+pub fn hello(") {
		t.Fatalf("rust rename missing declaration change:\n%s", combined)
	}
	if !strings.Contains(combined, "hello()") {
		t.Fatalf("rust rename missing call-site change:\n%s", combined)
	}
}
