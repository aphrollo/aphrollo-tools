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

func TestLaunchFor_ABinaryOutsideTheRootIsTheFallback(t *testing.T) {
	home := userbinTempHome(t)
	root, fb := LaunchFor(filepath.Join(home, "elsewhere", "aphrollo"))
	if root != userbinWantRoot(home) || fb != filepath.Join(home, "elsewhere", "aphrollo") {
		t.Fatalf("LaunchFor = %q, %q", root, fb)
	}
}

func TestLaunchFor_AVersionedBinaryIsNeverTheFallback(t *testing.T) {
	home := userbinTempHome(t)
	root, _ := Root()
	_, fb := LaunchFor(BinaryPath(root, "1.0.0"))
	if fb != legacyFallback {
		t.Fatalf("fallback = %q, want the legacy install path %q: a versioned dir is pruned", fb, legacyFallback)
	}
	t.Cleanup(func() { SetFallbackBin("") })
	SetFallbackBin(filepath.Join(home, "old", "aphrollo"))
	_, fb = LaunchFor(BinaryPath(root, "1.0.0"))
	if fb != filepath.Join(home, "old", "aphrollo") {
		t.Fatalf("fallback = %q, want the one named with SetFallbackBin", fb)
	}
}

func TestUnder_IsAboutPathContainmentOnly(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	for path, want := range map[string]bool{
		filepath.Join(root, "1.0.0", "aphrollo"): true,
		root:                                     false,
		root + "-other":                          false,
		filepath.Join(root, "..", "x"):           false,
		"":                                       false,
	} {
		if got := Under(root, path); got != want {
			t.Errorf("Under(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestInstall_RefusesAVersionThatIsNotADirectoryName(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	for _, v := range []string{"", "..", "a/b", `a\b`} {
		if _, err := Install(root, v, userbinStage(t, "x")); err == nil {
			t.Errorf("Install accepted version %q", v)
		}
	}
}

func TestVersions_IgnoresStagingDirsAndDirsWithoutABinary(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	if _, err := Install(root, "1.0.0", userbinStage(t, "x")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "1.1.0"), 0o755); err != nil { // no binary inside
		t.Fatal(err)
	}
	stage := filepath.Join(root, ".stage-1")
	userbinScript(t, BinaryPath(stage, ""), "")
	if got := strings.Join(Versions(root), " "); got != "1.0.0" {
		t.Fatalf("Versions = %q, want only 1.0.0", got)
	}
}

func TestLaunch_ReadsBackWhatACommandAndAShimCarry(t *testing.T) {
	for name, text := range map[string]string{
		"hook command":    HookCommand("/r/bin", "/it's/aphrollo", 9, "gate stop"),
		"hook, no budget": HookCommand("/r/bin", "/it's/aphrollo", 0, "gate stop"),
		"shim":            "#!/bin/sh\n" + Prelude("/r/bin", "/it's/aphrollo") + `exec "$x" gate stop` + "\n",
	} {
		root, fb, ok := Launch(text)
		if !ok || root != "/r/bin" || fb != "/it's/aphrollo" {
			t.Errorf("%s: Launch = %q, %q, %v", name, root, fb, ok)
		}
	}
	if _, _, ok := Launch(`"/usr/bin/aphrollo" gate stop`); ok {
		t.Error("Launch accepted an old-style command")
	}
}

// Windows ships its own timeout.exe, which waits for a keypress and takes no
// command: found first on PATH it must never be run as a budget.
func TestHookCommand_ATimeoutThatIsNotGNUIsNeverUsedAsABudget(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	fb := filepath.Join(t.TempDir(), "aphrollo"+ExeSuffix)
	userbinScript(t, fb, `echo "ran $*"`)
	fake := t.TempDir()
	userbinScript(t, filepath.Join(fake, "timeout"), `echo "ERROR: Invalid syntax" >&2; exit 1`)
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	code, out, _ := userbinRun(t, HookCommand(root, fb, 9, "gate stop"))
	if code != 0 || strings.TrimSpace(out) != "ran gate stop" {
		t.Fatalf("code %d, stdout %q; want the binary run with no budget", code, out)
	}
}

func TestWriteLauncher_WritesOnceAndFollowsThePointerLikeAHook(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	changed, err := WriteLauncher(root, "/old/aphrollo")
	if err != nil || !changed {
		t.Fatalf("first write: changed %v, err %v", changed, err)
	}
	if changed, err := WriteLauncher(root, "/old/aphrollo"); err != nil || changed {
		t.Fatalf("second write: changed %v, err %v; want a no-op", changed, err)
	}
	data, err := os.ReadFile(LauncherPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ReplaceAll(string(data), `\`, "/"), "/old/aphrollo") || !strings.Contains(string(data), "current") {
		t.Fatalf("launcher does not name the fallback and the pointer:\n%s", data)
	}
	userbinLauncherBehaves(t, root)
}

func TestPathCheck_SaysWhenAphrolloResolvesAnywhereButTheInstall(t *testing.T) {
	root := filepath.Join(userbinTempHome(t), "bin")
	if line := PathCheck(root); line != "" {
		t.Fatalf("with no user-space install there is nothing to say: %q", line)
	}
	p, _ := Install(root, "2.0.0", userbinStage(t, "x"))
	if err := SetCurrent(root, "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLauncher(root, ""); err != nil {
		t.Fatal(err)
	}
	old := lookPathFn
	t.Cleanup(func() { lookPathFn = old })
	for _, ok := range []string{LauncherPath(root), p} {
		lookPathFn = func(string) (string, error) { return ok, nil }
		if line := PathCheck(root); line != "" {
			t.Errorf("%s is the install, yet: %q", ok, line)
		}
	}
	lookPathFn = func(string) (string, error) { return "/usr/local/bin/aphrollo", nil }
	line := PathCheck(root)
	if !strings.Contains(line, "/usr/local/bin/aphrollo") || !strings.Contains(line, "first on PATH") || strings.Contains(line, "\n") {
		t.Fatalf("PathCheck = %q; want one line naming the resolved path and the fix", line)
	}
	lookPathFn = func(string) (string, error) { return "", errors.New("not found") }
	if line := PathCheck(root); !strings.Contains(line, "not on PATH") {
		t.Fatalf("PathCheck = %q; want it to say aphrollo is not on PATH", line)
	}
}
