package userbin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// userbinTempHome points every variable Root reads at one temp dir, so a test
// never sees the box's real user-space install.
func userbinTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	return home
}

func userbinStage(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "staged"+ExeSuffix)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoot_IsTheUserSpaceDirOfThePlatform(t *testing.T) {
	home := userbinTempHome(t)
	got, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := userbinWantRoot(home); got != want {
		t.Fatalf("Root() = %q, want %q", got, want)
	}
}

func TestInstall_WritesTheVersionDirAndLeavesOthersAlone(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	if _, err := Install(root, "1.0.0", userbinStage(t, "one")); err != nil {
		t.Fatal(err)
	}
	p2, err := Install(root, "1.1.0", userbinStage(t, "two"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "1.1.0", "aphrollo"+ExeSuffix); p2 != want {
		t.Fatalf("path = %q, want %q", p2, want)
	}
	b, err := os.ReadFile(BinaryPath(root, "1.0.0"))
	if err != nil || string(b) != "one" {
		t.Fatalf("1.0.0 binary = %q, %v; want it untouched", b, err)
	}
}

func TestSetCurrent_SwapsThePointerWhileTheOldBinaryIsOpen(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	p1, _ := Install(root, "1.0.0", userbinStage(t, "one"))
	if err := SetCurrent(root, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	// An open handle is what a running image is: on Windows it forbids
	// replacing or deleting the file, so the swap must not need to.
	held, err := os.Open(p1)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := Install(root, "1.1.0", userbinStage(t, "two")); err != nil {
		t.Fatalf("installing beside the running version: %v", err)
	}
	if err := SetCurrent(root, "1.1.0"); err != nil {
		t.Fatalf("swapping the pointer: %v", err)
	}
	if v, ok := Current(root); !ok || v != "1.1.0" {
		t.Fatalf("Current = %q, %v; want 1.1.0", v, ok)
	}
}

func TestVersions_SortBySemverNotByText(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	for _, v := range []string{"1.10.0", "1.2.0", "1.9.3"} {
		if _, err := Install(root, v, userbinStage(t, v)); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(Versions(root), " ")
	if want := "1.2.0 1.9.3 1.10.0"; got != want {
		t.Fatalf("Versions = %q, want %q", got, want)
	}
}

func TestPrune_KeepsTheNewestThreeAndTheCurrentOne(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0"} {
		if _, err := Install(root, v, userbinStage(t, v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetCurrent(root, "1.0.0"); err != nil { // switched back with --to
		t.Fatal(err)
	}
	removed, held := Prune(root, 3)
	if got := strings.Join(removed, " "); got != "1.1.0" {
		t.Fatalf("removed = %q, want only 1.1.0 (1.0.0 is current)", got)
	}
	if len(held) != 0 {
		t.Fatalf("held = %v, want none", held)
	}
	if got, want := strings.Join(Versions(root), " "), "1.0.0 1.2.0 1.3.0 1.4.0"; got != want {
		t.Fatalf("left = %q, want %q", got, want)
	}
}

func TestPrune_ReportsAVersionThePlatformWillNotLetGo(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"} {
		if _, err := Install(root, v, userbinStage(t, v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetCurrent(root, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	old := removeAllFn
	removeAllFn = func(string) error { return errors.New("the file is in use") }
	defer func() { removeAllFn = old }()
	removed, held := Prune(root, 3)
	if len(removed) != 0 || strings.Join(held, " ") != "1.0.0" {
		t.Fatalf("removed = %v, held = %v; want held [1.0.0]", removed, held)
	}
}

func TestResolve_PrefersTheUserSpaceCurrentThenTheFallback(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	fb := userbinStage(t, "legacy")
	if got, src := Resolve(root, fb); got != fb || src != SourceFallback {
		t.Fatalf("before any install: %q, %q; want the fallback", got, src)
	}
	p, _ := Install(root, "2.0.0", userbinStage(t, "new"))
	if err := SetCurrent(root, "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if got, src := Resolve(root, fb); got != p || src != SourceUser {
		t.Fatalf("after install: %q, %q; want %q user-space", got, src, p)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got, src := Resolve(root, fb); got != fb || src != SourceFallback {
		t.Fatalf("pointer to a missing binary: %q, %q; want the fallback", got, src)
	}
	if got, src := Resolve(root, filepath.Join(root, "nope")); got != "" || src != SourceNone {
		t.Fatalf("nothing runnable: %q, %q; want none", got, src)
	}
}

func userbinSh(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err) // skip-ok: the hook command is POSIX sh by contract
	}
	return sh
}

func userbinRun(t *testing.T, command string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(userbinSh(t), "-c", command)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String(), errb.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, out.String(), errb.String()
}

// userbinScript is a fake aphrollo: a sh script named like the binary.
func userbinScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHookCommand_RunsTheUserSpaceBinaryWhenThereIsOne(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	userbinScript(t, BinaryPath(root, "3.0.0"), `echo "user $*"; exit 0`)
	if err := SetCurrent(root, "3.0.0"); err != nil {
		t.Fatal(err)
	}
	fb := filepath.Join(t.TempDir(), "aphrollo"+ExeSuffix)
	userbinScript(t, fb, `echo "legacy $*"`)
	code, out, _ := userbinRun(t, HookCommand(root, fb, 0, "tdd stop"))
	if code != 0 || strings.TrimSpace(out) != "user tdd stop" {
		t.Fatalf("code %d, stdout %q; want the user-space binary", code, out)
	}
}

func TestHookCommand_FallsBackToTheInstalledPath(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	fb := filepath.Join(t.TempDir(), "aphrollo"+ExeSuffix)
	userbinScript(t, fb, `echo "legacy $*"`)
	code, out, _ := userbinRun(t, HookCommand(root, fb, 0, "tdd stop"))
	if code != 0 || strings.TrimSpace(out) != "legacy tdd stop" {
		t.Fatalf("code %d, stdout %q; want the fallback binary", code, out)
	}
}

func TestHookCommand_MissingBinaryIsANoOpThatExitsZero(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	cmd := HookCommand(root, filepath.Join(root, "gone"+ExeSuffix), 0, "tdd stop")
	code, out, errs := userbinRun(t, cmd)
	if code != 0 || out != "" {
		t.Fatalf("code %d, stdout %q; want a silent success", code, out)
	}
	if n := strings.Count(strings.TrimSpace(errs), "\n") + 1; n != 1 || !strings.Contains(errs, "skipped") {
		t.Fatalf("stderr %q; want exactly one line saying the hook was skipped", errs)
	}
}

func TestHookCommand_KeepsTheBinarysExitCode(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	fb := filepath.Join(t.TempDir(), "aphrollo"+ExeSuffix)
	userbinScript(t, fb, `echo refused >&2; exit 2`)
	code, _, errs := userbinRun(t, HookCommand(root, fb, 5, "tdd pretooluse"))
	if code != 2 || !strings.Contains(errs, "refused") {
		t.Fatalf("code %d, stderr %q; a deny from the binary must stay a deny", code, errs)
	}
}

func TestHookCommand_ABinaryOverItsBudgetIsANoOp(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skipf("no timeout(1): %v", err) // skip-ok: the budget is enforced by timeout(1) where the box has it
	}
	root := filepath.Join(userbinTempHome(t), "bin")
	fb := filepath.Join(t.TempDir(), "aphrollo"+ExeSuffix)
	userbinScript(t, fb, `sleep 30`)
	code, _, errs := userbinRun(t, HookCommand(root, fb, 1, "tdd stop"))
	if code != 0 || !strings.Contains(errs, "budget") {
		t.Fatalf("code %d, stderr %q; want exit 0 and a budget line", code, errs)
	}
}
