package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// gateInitFixture is one run's isolated world: an own Claude config dir, an
// own git hooks dir with an own global git config, and an own queue-shim dir.
// Every test below asserts on what init WROTE, so none of them may read or
// write the box's real ~/.config/git/hooks — the state this issue is about
// is precisely a box whose hooks point at a binary that is not there.
type gateInitFixture struct{ cfg, hooks, shimDir string }

func newGateInitFixture(t *testing.T) gateInitFixture {
	t.Helper()
	t.Chdir(t.TempDir())
	isolateGit(t)
	// The hooks dir sits under t.TempDir(), the dogfooding shape
	// installGitGate's temp/scratchpad refusal is meant to let through.
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	return gateInitFixture{
		cfg:     t.TempDir(),
		hooks:   filepath.Join(t.TempDir(), "githooks"),
		shimDir: filepath.Join(t.TempDir(), "cargo-queue"),
	}
}

func (f gateInitFixture) run(bin string, extra ...string) (int, string, string) {
	args := append([]string{"gate", "init", "--config-dir", f.cfg,
		"--git-hooks-dir", f.hooks, "--cargo-shim-dir", f.shimDir, "--bin", bin}, extra...)
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// wroteNothing asserts the refusal left the box exactly as it found it. A
// partial install is the state this guard exists to prevent: hooks pointing
// at a dead path are worse than no hooks, because git reports nothing.
func (f gateInitFixture) wroteNothing(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.hooks, "pre-commit")); !os.IsNotExist(err) {
		t.Errorf("a pre-commit shim was written despite the refusal (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(f.cfg, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings.json was written despite the refusal (stat err = %v)", err)
	}
	hp, _ := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	if got := strings.TrimSpace(string(hp)); got != "" {
		t.Errorf("core.hooksPath = %q, want it untouched by a refused init", got)
	}
}

// writeFakeBin writes a file that looks like an installed binary: present,
// executable, and (on Windows) carrying the extension that makes it
// spawnable. Its CONTENT never runs — the smoke check is stubbed for this
// package in TestMain.
func writeFakeBin(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeInstalledBin is the same stand-in under a dir of its own, forward
// slashed so it round-trips byte-identically through the shims' shellPath
// normalization. Every test in this package that installs hooks needs one:
// since issue #681 a --bin init cannot run is refused, and a path like
// /usr/local/bin/aphrollo does not exist on the box running the suite.
func fakeInstalledBin(t *testing.T) string {
	t.Helper()
	return filepath.ToSlash(writeFakeBin(t, filepath.Join(t.TempDir(), "aphrollo.exe")))
}

// A --bin that does not exist must be refused BEFORE anything is written.
// Accepting it wires every global git hook to a path that exits 127, and
// nothing on the box says the gate is gone (issue #681).
func TestGateInit_RefusesABinThatDoesNotExist(t *testing.T) {
	f := newGateInitFixture(t)
	missing := filepath.Join(t.TempDir(), "aphrollo.exe")

	code, _, errb := f.run(missing)
	if code == 0 {
		t.Fatalf("init exit = 0 for a --bin that does not exist, want non-zero\nstderr: %s", errb)
	}
	if !strings.Contains(errb, missing) {
		t.Errorf("the refusal must name the path it could not find, got:\n%s", errb)
	}
	f.wroteNothing(t)
}

// `which aphrollo` in Git Bash prints the extensionless form of a Windows
// executable, so passing it verbatim is the common case, not an edge one.
// init resolves it to the .exe beside it and says so, rather than installing
// a path nothing can spawn.
func TestGateInit_ResolvesAnExtensionlessBinToTheExeBesideIt(t *testing.T) {
	f := newGateInitFixture(t)
	binDir := t.TempDir()
	real := writeFakeBin(t, filepath.Join(binDir, "aphrollo.exe"))
	given := filepath.Join(binDir, "aphrollo")

	orig := binGOOS
	t.Cleanup(func() { binGOOS = orig })
	binGOOS = "windows"

	code, out, errb := f.run(given)
	if code != 0 {
		t.Fatalf("init exit = %d for an extensionless bin with a .exe beside it, want 0\nstderr: %s", code, errb)
	}
	if !strings.Contains(out, real) {
		t.Errorf("init must report the path it resolved to, got:\n%s", out)
	}
	shim, err := os.ReadFile(filepath.Join(f.hooks, "pre-commit"))
	if err != nil {
		t.Fatalf("pre-commit shim not installed: %v", err)
	}
	if !strings.Contains(string(shim), `"`+filepath.ToSlash(real)+`"`) {
		t.Errorf("the hook must exec the resolved .exe, got:\n%s", shim)
	}
}

// Existence is not runnability. `gate self-install` proves a candidate with a
// selfcheck before it swaps; init writing the hooks every commit on the box
// depends on gets the same proof, and refuses the same way.
func TestGateInit_RefusesABinThatFailsItsSmokeCheck(t *testing.T) {
	f := newGateInitFixture(t)
	bin := writeFakeBin(t, filepath.Join(t.TempDir(), "aphrollo.exe"))

	orig := runSmokeCheckFn
	t.Cleanup(func() { runSmokeCheckFn = orig })
	runSmokeCheckFn = func(string) error { return errors.New("FindProjectRoot regression") }

	code, _, errb := f.run(bin)
	if code == 0 {
		t.Fatalf("init exit = 0 for a --bin that failed its smoke check, want non-zero\nstderr: %s", errb)
	}
	if !strings.Contains(errb, "FindProjectRoot regression") {
		t.Errorf("the refusal must carry the smoke check's own failure, got:\n%s", errb)
	}
	f.wroteNothing(t)
}

// The mirror case: --uninstall is how a box whose binary is already gone gets
// its dead hooks removed, so the guard must never stand between it and that.
func TestGateInit_UninstallsWithABinThatIsAlreadyGone(t *testing.T) {
	f := newGateInitFixture(t)
	bin := writeFakeBin(t, filepath.Join(t.TempDir(), "aphrollo.exe"))
	if code, _, errb := f.run(bin); code != 0 {
		t.Fatalf("install exit = %d, want 0\nstderr: %s", code, errb)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}

	code, _, errb := f.run(bin, "--uninstall")
	if code != 0 {
		t.Fatalf("uninstall exit = %d with the binary already gone, want 0\nstderr: %s", code, errb)
	}
	if _, err := os.Stat(filepath.Join(f.hooks, "pre-commit")); !os.IsNotExist(err) {
		t.Errorf("the dead pre-commit shim survived uninstall (stat err = %v)", err)
	}
}
