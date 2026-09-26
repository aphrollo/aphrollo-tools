package tddtest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A fake `cargo` on PATH that answers to the name but cannot run — standing
// in for the aphrollo build-queue shim when it fails to resolve the real
// toolchain under an isolated CARGO_HOME. It always exits 1, no matter what
// CARGO_HOME says, printing the same shape of complaint the real shim prints.
func writeUnresolvableCargoShim(t *testing.T, dir string) {
	t.Helper()
	name := "cargo"
	body := "#!/bin/sh\necho 'aphrollo tdd cargo: resolve cargo: not found' >&2\nexit 1\n"
	if runtime.GOOS == "windows" {
		name = "cargo.bat"
		body = "@echo off\r\necho aphrollo tdd cargo: resolve cargo: not found 1>&2\r\nexit /b 1\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestRealCargoAvailable_AFakeShimThatCannotResolveIsUnavailable is the
// mechanical proof behind RequireRealCargo: a `cargo` that resolves by name
// but fails to run must never read as "the toolchain is here". Issue found
// via #853 — a bare exec.LookPath("cargo") check passes for exactly this
// shim, and the shim's own stderr then stands in for whatever the caller
// actually wanted to run.
func TestRealCargoAvailable_AFakeShimThatCannotResolveIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	writeUnresolvableCargoShim(t, dir)

	ok, detail := realCargoAvailable([]string{"PATH=" + dir})

	if ok {
		t.Fatalf("realCargoAvailable said true for a fake shim that cannot resolve; detail=%q", detail)
	}
	if detail == "" {
		t.Fatal("realCargoAvailable reported no detail for an unavailable cargo")
	}
}

// TestRealCargoAvailable_NoCargoOnThePATHIsUnavailable is lookPathIn's other
// branch: a PATH whose directories hold no `cargo` at all, rather than one
// that resolves to something unrunnable. An empty directory and a directory
// that does not exist are both real shapes a caller's env can carry — the
// loop must walk past the missing one rather than stopping on it — and
// neither must ever be answered by whatever `cargo` this process's own PATH
// happens to resolve (the box's build-queue shim, on an operator box), which
// is exactly the bug a bare exec.Command("cargo") with cmd.Env set had.
func TestRealCargoAvailable_NoCargoOnThePATHIsUnavailable(t *testing.T) {
	empty := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	env := []string{"PATH=" + empty + string(os.PathListSeparator) + missing}

	ok, detail := realCargoAvailable(env)

	if ok {
		t.Fatalf("realCargoAvailable said true with no cargo anywhere on PATH; detail=%q", detail)
	}
	if want := "cargo: not found in PATH"; detail != want {
		t.Fatalf("detail = %q, want %q", detail, want)
	}
}

// TestRequireRealCargo_AFakeShimThatCannotResolveSkips proves the exported
// helper itself: given nothing but the fake shim on PATH, it must SKIP the
// caller's test rather than let it proceed as if a real cargo answered.
// Skipping is asserted through the subtest's own *testing.T — a caller whose
// body runs past RequireRealCargo without it ever calling Skip would fail via
// the t.Fatal immediately below the call, which turns this test itself red.
func TestRequireRealCargo_AFakeShimThatCannotResolveSkips(t *testing.T) {
	dir := t.TempDir()
	writeUnresolvableCargoShim(t, dir)
	t.Setenv("PATH", dir)
	t.Setenv("CARGO_HOME", filepath.Join(dir, "empty-cargo-home"))

	var inner *testing.T
	t.Run("probe", func(st *testing.T) {
		inner = st
		RequireRealCargo(st)
		st.Fatal("RequireRealCargo did not skip for an unresolvable fake shim")
	})
	if !inner.Skipped() {
		t.Fatalf("RequireRealCargo did not skip (skipped=%v, failed=%v)", inner.Skipped(), inner.Failed())
	}
	if inner.Failed() {
		t.Fatal("RequireRealCargo's subtest failed instead of skipping cleanly")
	}
}
