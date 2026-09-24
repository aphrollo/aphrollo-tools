package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultCargoShimDir_NonWindows_IsPerUserDataDir proves the fix for the
// deploy-owned bin dir: the default must be the per-user dir every session's
// PATH is actually configured to prepend (aphrollo-infra pins
// ~/.local/share/aphrollo/cargo-queue by hand for exactly this reason),
// independent of where the binary itself lives.
func TestDefaultCargoShimDir_NonWindows_IsPerUserDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := DefaultCargoShimDir("/opt/aphrollo-cli/releases/20260101-abcdef/aphrollo", "linux")
	want := filepath.Join(home, ".local", "share", "aphrollo", "cargo-queue")
	if got != want {
		t.Errorf("DefaultCargoShimDir = %q, want %q", got, want)
	}
}

// TestDefaultCargoShimDir_Windows_StaysAlongsideBin proves the existing
// Windows convention is untouched: self-install already places the binary
// under the user's own bin dir, so the sibling cargo-queue dir is already
// writable and per-user there.
func TestDefaultCargoShimDir_Windows_StaysAlongsideBin(t *testing.T) {
	bin := `C:\Users\olive\bin\aphrollo.exe`
	got := DefaultCargoShimDir(bin, "windows")
	want := filepath.Join(filepath.Dir(bin), "cargo-queue")
	if got != want {
		t.Errorf("DefaultCargoShimDir = %q, want %q", got, want)
	}
}

// TestShimCommandName_AppendsExeOnWindowsOnly proves the platform split: only
// Windows resolves a bare command by extension (matching internal/cli's own
// commandExeNames reasoning), so only there does the shim query need one.
func TestShimCommandName_AppendsExeOnWindowsOnly(t *testing.T) {
	if got := shimCommandName("git", "windows"); got != "git.exe" {
		t.Errorf("shimCommandName(windows) = %q, want git.exe", got)
	}
	if got := shimCommandName("git", "linux"); got != "git" {
		t.Errorf("shimCommandName(linux) = %q, want git", got)
	}
}

// TestShimBypassLine_SilentWhenShimNeverInstalled proves the opt-in
// contract: a shim dir holding nothing is not a defect, it's a box that
// never took the opt-in — a raw git/cargo on PATH is expected there.
func TestShimBypassLine_SilentWhenShimNeverInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restore := SetShimBypassLookPathForTest(func(string) (string, error) {
		return "/usr/bin/git", nil
	})
	defer restore()

	if line := ShimBypassLine("/opt/aphrollo/aphrollo"); line != "" {
		t.Fatalf("expected silence with no shim installed, got %q", line)
	}
}

// TestShimBypassLine_WarnsWhenLookPathEscapesTheShimDir is the repro: the
// shim IS installed, but this process's PATH resolves `cargo` outside it —
// a session that started before a PATH change took effect.
func TestShimBypassLine_WarnsWhenLookPathEscapesTheShimDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shimDir := filepath.Join(home, ".local", "share", "aphrollo", "cargo-queue")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "cargo"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := SetShimBypassLookPathForTest(func(name string) (string, error) {
		if name == "cargo" {
			return "/usr/bin/cargo", nil
		}
		return filepath.Join(shimDir, name), nil
	})
	defer restore()

	line := ShimBypassLine("/opt/aphrollo/aphrollo")
	if line == "" {
		t.Fatal("expected a warning when cargo resolves outside the installed shim dir")
	}
	for _, want := range []string{"cargo -> /usr/bin/cargo", shimDir, "aphrollo install"} {
		if !strings.Contains(line, want) {
			t.Errorf("line missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, "git -> ") {
		t.Errorf("line names git, which resolved INSIDE the shim dir: %s", line)
	}
}

// TestShimBypassLine_SilentWhenBothResolveInsideTheShimDir proves the
// healthy case: shims installed, PATH already picks them up — nothing to say.
func TestShimBypassLine_SilentWhenBothResolveInsideTheShimDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shimDir := filepath.Join(home, ".local", "share", "aphrollo", "cargo-queue")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "cargo"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := SetShimBypassLookPathForTest(func(name string) (string, error) {
		return filepath.Join(shimDir, name), nil
	})
	defer restore()

	if line := ShimBypassLine("/opt/aphrollo/aphrollo"); line != "" {
		t.Fatalf("expected silence when both resolve inside the shim dir, got %q", line)
	}
}

// TestHandleSessionStart_IncludesTheShimBypassLine proves the wiring: a
// non-empty ShimBypassLine finding reaches the session-start message.
func TestHandleSessionStart_IncludesTheShimBypassLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	restore := SetShimBypassLineForTest(func(string) string { return "cargo -> /usr/bin/cargo do not resolve into the shim dir" })
	defer restore()

	msg := HandleSessionStart([]byte(`{"session_id":"ss-shimbypass"}`))
	if !strings.Contains(msg, "cargo -> /usr/bin/cargo do not resolve into the shim dir") {
		t.Fatalf("session-start message does not include the shim bypass line:\n%s", msg)
	}
}
