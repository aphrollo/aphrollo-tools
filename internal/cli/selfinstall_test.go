package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Replacing the gate binary is the one upgrade that cannot be done the
// obvious way: on Windows the running exe cannot be deleted or overwritten,
// only RENAMED aside. These pin the ordering that makes that work, and the
// sweep that stops the renamed copies accumulating forever.

// selfInstallFixture lays out a bin dir holding a current binary and stubs the
// build so no toolchain runs. It returns the bin path and the dir.
func selfInstallFixture(t *testing.T, built string) (bin string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte(built), 0o755); err != nil {
			return "", err
		}
		return "go build -buildvcs=false -o " + out + " ./cmd/aphrollo", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })
	return bin
}

func TestSelfInstall_RenamesTheRunningBinaryAsideAndMovesTheNewOneIn(t *testing.T) {
	bin := selfInstallFixture(t, "NEW")
	var out, errb bytes.Buffer

	if code := runGateSelfInstall([]string{"--bin", bin, "--repo", t.TempDir(), "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("self-install exit = %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("the binary is gone after a self-install: %v", err)
	}
	if string(got) != "NEW" {
		t.Fatalf("binary content = %q, want the freshly built one", got)
	}
	stale := staleCopies(t, filepath.Dir(bin))
	if len(stale) != 1 {
		t.Fatalf("the replaced binary must be renamed aside, found %v", stale)
	}
	if body, err := os.ReadFile(stale[0]); err != nil || string(body) != "OLD" {
		t.Fatalf("the stale copy must hold the binary that was replaced, got %q (%v)", body, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(bin), "aphrollo.new.exe")); err == nil {
		t.Fatal("the staging copy must be moved into place, not left behind")
	}
	// One line per step, so an operator can see which one failed.
	for _, want := range []string{"build", "rename", "move", "sweep"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not report the %q step:\n%s", want, out.String())
		}
	}
}

func TestSelfInstall_SweepsTheStaleCopiesTheLastUpgradeLeft(t *testing.T) {
	bin := selfInstallFixture(t, "NEW")
	dir := filepath.Dir(bin)
	old := filepath.Join(dir, "aphrollo.stale-1700000000.exe")
	if err := os.WriteFile(old, []byte("OLDER"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := runGateSelfInstall([]string{"--bin", bin, "--repo", t.TempDir(), "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("self-install exit = %d\nstderr:%s", code, errb.String())
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("an unlocked stale copy from an earlier upgrade must be reclaimed")
	}
	if got := staleCopies(t, dir); len(got) != 1 {
		t.Fatalf("only this run's stale copy should survive, found %v", got)
	}
}

// selfInstallBuildStub swaps buildAphrollo for one that writes built to out
// and restores it at test end — the mutation-proof twin of
// selfInstallFixture for tests that lay out the bin dir themselves.
func selfInstallBuildStub(t *testing.T, built string) {
	t.Helper()
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte(built), 0o755); err != nil {
			return "", err
		}
		return "go build -buildvcs=false -o " + out + " ./cmd/aphrollo", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })
}

// #366: an explicit --bin with no extension on Windows must still land the
// build somewhere exec.LookPath (and everything Go spawns) can find it, not
// only a human's shell.
func TestSelfInstall_NormalizesAnExtensionlessBinFlagToExe(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "aphrollo") // deliberately no extension
	selfInstallBuildStub(t, "NEW")

	var out, errb bytes.Buffer
	if code := runGateSelfInstall([]string{"--bin", bin, "--repo", t.TempDir(), "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("self-install exit = %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	wantBin := bin + ".exe"
	got, err := os.ReadFile(wantBin)
	if err != nil {
		t.Fatalf("%s does not exist after install: %v", wantBin, err)
	}
	if string(got) != "NEW" {
		t.Fatalf("%s content = %q, want the freshly built one", wantBin, got)
	}
	if _, err := os.Stat(bin); err == nil {
		t.Fatalf("an extensionless %s must not exist beside %s", bin, wantBin)
	}
}

// #366: the outage traced to this exact step — with the extension dropped,
// swapBinary stat'd the wrong name, found nothing, and swept the good
// binary instead of renaming it aside. This pins that once bin is
// normalized, the pre-existing .exe IS found and preserved.
func TestSelfInstall_RenamesThePreExistingExeAsideWhenBinFlagOmitsTheExtension(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aphrollo.exe")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "aphrollo") // the flag as a caller who forgot the extension would pass it
	selfInstallBuildStub(t, "NEW")

	var out, errb bytes.Buffer
	if code := runGateSelfInstall([]string{"--bin", bin, "--repo", t.TempDir(), "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("self-install exit = %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "rename skipped") {
		t.Fatalf("the pre-existing binary was not found under its real name, output:\n%s", out.String())
	}
	stale := staleCopies(t, dir)
	if len(stale) != 1 {
		t.Fatalf("the replaced binary must be renamed aside, found %v", stale)
	}
	if body, err := os.ReadFile(stale[0]); err != nil || string(body) != "OLD" {
		t.Fatalf("the stale copy must hold the previous binary, got %q (%v)", body, err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "NEW" {
		t.Fatalf("%s = %q (%v), want the freshly built bytes", exe, got, err)
	}
}

// #366's fix must apply to both installers from one place: this exercises
// the shared normalization directly, including the non-Windows case that
// this box cannot exercise by actually switching GOOS.
func TestBinExtForOS_AppendsExeOnlyOnWindowsWhenBinHasNoExtension(t *testing.T) {
	cases := []struct {
		name     string
		bin      string
		goos     string
		want     string
		appended bool
	}{
		{"windows extensionless", `C:\bin\aphrollo`, "windows", `C:\bin\aphrollo.exe`, true},
		{"windows already has .exe", `C:\bin\aphrollo.exe`, "windows", `C:\bin\aphrollo.exe`, false},
		{"linux extensionless is unchanged", "/usr/local/bin/aphrollo", "linux", "/usr/local/bin/aphrollo", false},
		{"darwin extensionless is unchanged", "/usr/local/bin/aphrollo", "darwin", "/usr/local/bin/aphrollo", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, appended := binExtForOS(c.bin, c.goos)
			if got != c.want || appended != c.appended {
				t.Fatalf("binExtForOS(%q, %q) = (%q, %v), want (%q, %v)", c.bin, c.goos, got, appended, c.want, c.appended)
			}
		})
	}
}

func TestSelfInstall_KeepsAStaleCopyItCannotDelete(t *testing.T) {
	bin := selfInstallFixture(t, "NEW")
	dir := filepath.Dir(bin)
	// A stale path that cannot be removed stands in for the copy Windows is
	// still holding open: the sweep must report it, not fail the upgrade.
	locked := filepath.Join(dir, "aphrollo.stale-1700000001.exe")
	if err := os.MkdirAll(filepath.Join(locked, "held"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := runGateSelfInstall([]string{"--bin", bin, "--repo", t.TempDir(), "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("a stale copy still in use must not fail the upgrade, exit = %d\nstderr:%s", code, errb.String())
	}
	if _, err := os.Stat(locked); err != nil {
		t.Fatalf("the locked copy should be left alone: %v", err)
	}
	if got, err := os.ReadFile(bin); err != nil || string(got) != "NEW" {
		t.Fatalf("the upgrade must still have landed, got %q (%v)", got, err)
	}
}

// A binary replaced without rewiring leaves settings.json pointing at a build
// that is no longer there, so the run has to finish the job.
func TestSelfInstall_RewiresTheHooksAtTheNewBinary(t *testing.T) {
	isolateGit(t)
	cfg := gateConfigDir(t)
	t.Chdir(t.TempDir()) // init patches the CWD repo's CLAUDE.md — never this repo's
	bin := selfInstallFixture(t, "NEW")

	var out, errb bytes.Buffer
	// --git-hooks-dir is forwarded to init: without it the run would rewrite
	// this box's own managed hooks to point at a temp binary.
	args := []string{"--bin", bin, "--repo", t.TempDir(), "--", "--git-hooks-dir", t.TempDir()}
	if code := runGateSelfInstall(args, &out, &errb); code != 0 {
		t.Fatalf("self-install exit = %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	settings, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatalf("self-install did not run init: %v", err)
	}
	if !strings.Contains(string(settings), "gate pretooluse") {
		t.Fatalf("the hooks must be rewired at the new binary, got:\n%s", settings)
	}
}

func TestSelfInstall_LeavesTheBinaryAloneWhenTheBuildFails(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		return "go build", errors.New("cmd/aphrollo does not compile")
	}
	t.Cleanup(func() { buildAphrollo = prev })

	var out, errb bytes.Buffer
	if code := runGateSelfInstall([]string{"--bin", bin, "--repo", dir, "--no-init"}, &out, &errb); code == 0 {
		t.Fatal("a failed build must not report success")
	}
	if got, err := os.ReadFile(bin); err != nil || string(got) != "OLD" {
		t.Fatalf("a failed build must leave the installed binary untouched, got %q (%v)", got, err)
	}
	if len(staleCopies(t, dir)) != 0 {
		t.Fatal("nothing may be renamed aside before the new binary exists")
	}
}

// buildArgs is what buildAphrollo actually invokes `go` with. Stamping the
// commit and build time through -ldflags -X is the whole point of this
// lane: a binary built with -buildvcs=false otherwise has no idea what it
// is (see internal/buildinfo).
func TestBuildArgs_StampsCommitAndBuildTimeThroughLdflags(t *testing.T) {
	got := buildArgs("/r", "/o", "ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2", time.Date(2026, 9, 5, 4, 57, 0, 0, time.FixedZone("CEST", 2*3600)))
	want := []string{
		"build", "-buildvcs=false",
		"-ldflags", "-X github.com/aphrollo/aphrollo-tools/internal/buildinfo.commit=ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2 -X github.com/aphrollo/aphrollo-tools/internal/buildinfo.builtAt=2026-09-05T02:57:00Z",
		"-o", "/o",
		"./cmd/aphrollo",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs = %#v, want %#v", got, want)
	}
}

// staleCopies lists the renamed-aside binaries in dir.
func staleCopies(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "aphrollo.stale-") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
