package tdd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeBinary writes a stand-in for the aphrollo binary and returns its path.
func fakeBinary(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "aphrollo.exe")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInstallShimExes_CopiesTheBinaryUnderEachShimName pins what replaces the
// batch shims: a real executable per tool name, byte-identical to the aphrollo
// binary, so the argv it receives is the caller's own. A .cmd file cannot do
// this — cmd.exe strips `^` from an argument and re-splits quoted ones.
func TestInstallShimExes_CopiesTheBinaryUnderEachShimName(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	src := fakeBinary(t, base, "APHROLLO-V1")
	dir := filepath.Join(base, "cargo-queue")

	res, err := installShimExes(dir, src, []string{"cargo.exe", "git.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Installed, []string{"cargo.exe", "git.exe"}) {
		t.Fatalf("Installed = %v, want both names on a fresh install", res.Installed)
	}
	if len(res.Locked) != 0 {
		t.Fatalf("Locked = %v, want none", res.Locked)
	}
	for _, name := range []string{"cargo.exe", "git.exe"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		if string(got) != "APHROLLO-V1" {
			t.Fatalf("%s = %q, want a copy of the binary", name, got)
		}
	}
}

// TestInstallShimExes_SecondCallWithTheSameBinaryCopiesNothing keeps `gate
// init` cheap and quiet: an unchanged binary must not recopy 20 MB twice per
// run, and must report nothing installed.
func TestInstallShimExes_SecondCallWithTheSameBinaryCopiesNothing(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	src := fakeBinary(t, base, "APHROLLO-V1")
	dir := filepath.Join(base, "cargo-queue")

	if _, err := installShimExes(dir, src, []string{"cargo.exe"}); err != nil {
		t.Fatal(err)
	}
	res, err := installShimExes(dir, src, []string{"cargo.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Installed) != 0 {
		t.Fatalf("Installed = %v, want none — the binary did not change", res.Installed)
	}
}

// TestInstallShimExes_RefreshesAStaleCopy is the update-in-place half: a
// rebuilt aphrollo binary must reach the shims, or every session keeps running
// last week's gate under the name `cargo`.
func TestInstallShimExes_RefreshesAStaleCopy(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	src := fakeBinary(t, base, "APHROLLO-V1")
	dir := filepath.Join(base, "cargo-queue")
	if _, err := installShimExes(dir, src, []string{"cargo.exe"}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(src, []byte("APHROLLO-V2-LONGER"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := installShimExes(dir, src, []string{"cargo.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Installed, []string{"cargo.exe"}) {
		t.Fatalf("Installed = %v, want cargo.exe refreshed", res.Installed)
	}
	got, err := os.ReadFile(filepath.Join(dir, "cargo.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "APHROLLO-V2-LONGER" {
		t.Fatalf("cargo.exe = %q, want the NEW binary", got)
	}
}

// TestInstallShimExes_MissingSourceIsAnError guards the silent-nothing case: a
// bin path that does not exist must be reported, never reported as installed.
func TestInstallShimExes_MissingSourceIsAnError(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	_, err := installShimExes(filepath.Join(base, "q"), filepath.Join(base, "nope.exe"), []string{"cargo.exe"})
	if err == nil {
		t.Fatal("a missing source binary must be an error")
	}
}

// TestRemoveCmdShims_DeletesTheBatchShims pins the retirement: the .cmd files
// are actively harmful (cmd.exe eats `^`, so `git rev-parse MERGE_HEAD^{tree}`
// and nextest's `-E test(/^mod::/)` both arrive mangled), so init deletes them
// and says so once. A file that is not one of the two names is left alone.
func TestRemoveCmdShims_DeletesTheBatchShims(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"cargo.cmd", "git.cmd", "rustup.cmd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := RemoveCmdShims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{"cargo.cmd", "git.cmd"}) {
		t.Fatalf("removed = %v, want the two batch shims", removed)
	}
	for _, name := range []string{"cargo.cmd", "git.cmd"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still present", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "rustup.cmd")); err != nil {
		t.Fatalf("a foreign .cmd must be left alone: %v", err)
	}
	// A second run has nothing to say.
	again, err := RemoveCmdShims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second run reported %v, want nothing", again)
	}
}

// TestInstallCargoShim_WritesNoBatchFile is the other side of the retirement:
// the installer must not put back what RemoveCmdShims deletes.
func TestInstallCargoShim_WritesNoBatchFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "cargo-queue")
	if _, err := InstallCargoShim(dir, `C:\Users\olive\bin\aphrollo.exe`); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallGitShim(dir, `C:\Users\olive\bin\aphrollo.exe`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cargo.cmd", "git.cmd"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was written — the batch shims are retired", name)
		}
	}
}
