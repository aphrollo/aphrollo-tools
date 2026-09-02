package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
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
