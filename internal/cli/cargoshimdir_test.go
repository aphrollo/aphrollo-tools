package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestDefaultCargoShimDir_NonWindows_IsPerUserDataDir proves the fix for the
// deploy-owned bin dir: `aphrollo install` used to derive the default queue
// shim dir from --bin's own directory, which on this box is
// /opt/aphrollo-cli/releases/<ts>-<sha>/, github-runner-owned — `mkdir
// .../cargo-queue: permission denied`. The default must instead be the
// per-user dir every session's PATH is actually configured to prepend
// (aphrollo-infra pins ~/.local/share/aphrollo/cargo-queue by hand for
// exactly this reason), independent of where the binary itself lives.
func TestDefaultCargoShimDir_NonWindows_IsPerUserDataDir(t *testing.T) {
	prev := binGOOS
	binGOOS = "linux"
	t.Cleanup(func() { binGOOS = prev })

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no resolvable home dir") // skip-ok: nothing to resolve the default against
	}
	got := defaultCargoShimDir("/opt/aphrollo-cli/releases/20260101-abcdef/aphrollo")
	want := filepath.Join(home, ".local", "share", "aphrollo", "cargo-queue")
	if got != want {
		t.Errorf("defaultCargoShimDir = %q, want %q", got, want)
	}
}

// TestDefaultCargoShimDir_Windows_StaysAlongsideBin proves the existing
// Windows convention is untouched: self-install already places the binary
// under the user's own bin dir (e.g. C:/Users/<user>/bin/aphrollo.exe), so
// the sibling cargo-queue dir is already writable and per-user — the
// managed CLAUDE.md block's own example.
func TestDefaultCargoShimDir_Windows_StaysAlongsideBin(t *testing.T) {
	prev := binGOOS
	binGOOS = "windows"
	t.Cleanup(func() { binGOOS = prev })

	bin := `C:\Users\olive\bin\aphrollo.exe`
	got := defaultCargoShimDir(bin)
	want := filepath.Join(filepath.Dir(bin), "cargo-queue")
	if got != want {
		t.Errorf("defaultCargoShimDir = %q, want %q", got, want)
	}
}

// TestRun_Install_DefaultShimDir_SucceedsWhenBinDirIsUnwritable is the actual
// repro: a --bin under a directory this account cannot write to (mirroring
// this box's CI-owned /opt/aphrollo-cli/releases/<ts>-<sha>/) must no longer
// make `aphrollo install` skip the queue shims — the default must land in
// the per-user dir instead.
func TestRun_Install_DefaultShimDir_SucceedsWhenBinDirIsUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not deny directory writes on Windows") // skip-ok: this test is about a POSIX permission bit
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions") // skip-ok: root would pass this check trivially and prove nothing
	}
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := t.TempDir()
	gitInitRepo(t, repo)

	binDir := t.TempDir()
	bin := writeFakeBin(t, filepath.Join(binDir, "aphrollo.exe"))
	if err := os.Chmod(binDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(binDir, 0o700) })

	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")

	var out, errb bytes.Buffer
	code := Run([]string{"install", "--repo", repo, "--config-dir", cfg, "--git-hooks-dir", hooks, "--bin", bin},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("install exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if strings.Contains(errb.String(), "skipped queue shims") {
		t.Fatalf("queue shims were skipped despite the writable default dir:\n%s", errb.String())
	}
	want := defaultCargoShimDir(bin)
	if _, err := os.Stat(filepath.Join(want, "cargo")); err != nil {
		t.Fatalf("cargo shim not written under the default dir %s: %v", want, err)
	}
}

// TestRun_Install_DefaultShimDir_RefreshesOnRerun proves a second `aphrollo
// install` (still no --cargo-shim-dir) picks up a moved binary and rewrites
// the shims already sitting under the default dir, rather than leaving them
// pointed at a stale path forever.
func TestRun_Install_DefaultShimDir_RefreshesOnRerun(t *testing.T) {
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := t.TempDir()
	gitInitRepo(t, repo)

	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	binDir := t.TempDir()

	run := func(bin string) (string, string) {
		var out, errb bytes.Buffer
		code := Run([]string{"install", "--repo", repo, "--config-dir", cfg, "--git-hooks-dir", hooks, "--bin", bin},
			strings.NewReader(""), &out, &errb)
		if code != 0 {
			t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
		}
		return out.String(), errb.String()
	}

	binOld := writeFakeBin(t, filepath.Join(binDir, "aphrollo-old.exe"))
	out1, _ := run(binOld)
	if !strings.Contains(out1, "installed cargo-queue shim") {
		t.Fatalf("first run did not report installing the cargo-queue shim:\n%s", out1)
	}
	shimDir := defaultCargoShimDir(binOld)
	data1, err := os.ReadFile(filepath.Join(shimDir, "cargo"))
	if err != nil {
		t.Fatalf("cargo shim not written: %v", err)
	}

	binNew := writeFakeBin(t, filepath.Join(binDir, "aphrollo-new.exe"))
	out2, _ := run(binNew)
	if !strings.Contains(out2, "installed cargo-queue shim") {
		t.Fatalf("second run did not report refreshing the cargo-queue shim:\n%s", out2)
	}
	data2, err := os.ReadFile(filepath.Join(shimDir, "cargo"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data1) == string(data2) {
		t.Fatal("cargo shim content did not change after the bin path changed")
	}
	if !strings.Contains(string(data2), filepath.ToSlash(binNew)) {
		t.Fatalf("cargo shim does not reference the new bin path:\n%s", data2)
	}
}
