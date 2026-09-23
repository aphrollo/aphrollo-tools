package tddtest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A test that wants a git repo used to build one: `git init` plus three `git
// config` calls plus an add and a commit, six process spawns, 178 times for
// the Go repo alone. On Linux a spawn is cheap enough not to notice; on
// Windows it is ~65 ms of real git and ~100 ms through the queue shim, and
// those spawns were the package's whole wall time (457 s of a 472 s run,
// measured 2026-09-03).
//
// So the repos are built ONCE, before any test runs, and each test gets a
// byte copy. A copy is a repo in its own right — separate objects, separate
// index, separate HEAD — which is the only property the 178 callers ever
// wanted from their own `git init`.
var (
	// initFixture is an initialised repo with no commit: the `git init` +
	// identity config that GitInit used to spawn four processes for.
	initFixture string
	// goFixture and cargoFixture add the committed base each helper's
	// callers expect, and with it the runner marker (go.mod / Cargo.toml)
	// that DetectRunner reads.
	goFixture    string
	cargoFixture string
)

// InitFixture is the initialised, commit-less golden repo Main built.
func InitFixture() string { return initFixture }

// buildFixtures builds the golden repos under dir. It runs from Main, so it
// takes no *testing.T and panics rather than failing a test: a package whose
// fixtures cannot be built has no test that can pass.
func buildFixtures(dir string) {
	// Same isolation GitInit gives each test, applied once for the build:
	// the operator's global config carries core.hooksPath (the gate), a
	// signing key and an init.defaultBranch, none of which a fixture wants.
	// Restored before the run so per-test isolation is unchanged.
	restore := isolateGitConfigEnv(filepath.Join(dir, "fixture-gitconfig"))
	defer restore()

	initFixture = filepath.Join(dir, "fixture-init")
	mustInitRepo(initFixture)

	goFixture = filepath.Join(dir, "fixture-go")
	MustCopyDir(goFixture, initFixture)
	mustWriteFile(filepath.Join(goFixture, "go.mod"), "module example.com/m\n\ngo 1.26\n")
	mustWriteFile(filepath.Join(goFixture, "doc.go"), "package m\n")
	mustGit(goFixture, "add", ".")
	mustGit(goFixture, "commit", "-qm", "base")

	cargoFixture = filepath.Join(dir, "fixture-cargo")
	MustCopyDir(cargoFixture, initFixture)
	mustWriteFile(filepath.Join(cargoFixture, "Cargo.toml"), "[package]\nname = \"m\"\nversion = \"0.1.0\"\n")
	mustWriteFile(filepath.Join(cargoFixture, "src", "lib.rs"), "pub fn base() -> i32 { 0 }\n")
	mustGit(cargoFixture, "add", ".")
	mustGit(cargoFixture, "commit", "-qm", "base")
}

// isolateGitConfigEnv is IsolateGitConfig without a *testing.T: it drops the
// repo-pointing GIT_* vars a hook exports and points git at an empty global
// config, returning the undo.
func isolateGitConfigEnv(configPath string) func() {
	var undo []func()
	for _, k := range []string{
		"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE",
		"GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX",
		"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
	} {
		if v, ok := os.LookupEnv(k); ok {
			undo = append(undo, func() { os.Setenv(k, v) })
		} else {
			undo = append(undo, func() { os.Unsetenv(k) })
		}
		os.Unsetenv(k)
	}
	mustWriteFile(configPath, "")
	os.Setenv("GIT_CONFIG_GLOBAL", configPath)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	return func() {
		for _, f := range undo {
			f()
		}
	}
}

// mustInitRepo is the four spawns every fixture used to pay, paid once.
func mustInitRepo(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		mustGit(dir, args...)
	}
	// The sample hooks are inert and are ~13 files every copy would carry.
	// The directory stays: a test that installs a hook writes into it.
	samples, _ := filepath.Glob(filepath.Join(dir, ".git", "hooks", "*.sample"))
	for _, s := range samples {
		if err := os.Remove(s); err != nil {
			panic(err)
		}
	}
}

func mustGit(dir string, args ...string) {
	cmd := exec.Command(gitBin(), args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("fixture git " + filepath.Join(args...) + ": " + err.Error() + "\n" + string(out))
	}
}

func mustWriteFile(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		panic(err)
	}
}

// MustCopyDir copies a built fixture into a fresh directory. dst must not hold
// any of src's files yet — os.CopyFS refuses to overwrite, which is the check
// that a fixture is never handed out twice.
func MustCopyDir(dst, src string) {
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		panic(err)
	}
}

// FixtureTargetUnderTemp reports why dst is not a place a fixture repository
// may be built: nowhere at all (git would use the process's own working
// directory), or anywhere outside the OS temp dir.
func FixtureTargetUnderTemp(dst string) error {
	if strings.TrimSpace(dst) == "" {
		return errors.New("fixture target is empty — git would build the fixture in the process's own working directory")
	}
	abs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(tmp, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("fixture target %s is not under %s — a fixture repository is only ever built in a temp dir", abs, tmp)
	}
	return nil
}

// CopyFixture hands a test its own copy of one of the golden repos.
func CopyFixture(t *testing.T, dst, src string) string {
	t.Helper()
	if err := FixtureTargetUnderTemp(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dst
}

// GitInit gives dir the .git of an initialised repo with the fixture identity
// configured — the four spawns it used to cost, copied from the golden repo
// Main built once.
func GitInit(t *testing.T, dir string) {
	t.Helper()
	// Isolate git config so the operator box's global core.hooksPath (the
	// aphrollo tdd gate) does not recurse into this fixture's setup commits.
	IsolateGitConfig(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	CopyFixture(t, dir, initFixture)
}

// MakeGoRepo hands the test its own copy of the committed Go module Main
// built once: go.mod, doc.go, one commit, a clean worktree.
func MakeGoRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath(gitBin()); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	IsolateGitConfig(t)
	return CopyFixture(t, root, goFixture)
}

// MakeCargoRepo creates a committed Rust crate whose root marker is Cargo.toml.
// Like makeJSRepo, the suite is always faked (cargo need not be installed) —
// only DetectRunner's marker read and the git state matter.
func MakeCargoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	IsolateGitConfig(t)
	return CopyFixture(t, root, cargoFixture)
}

// IsolateGitConfig points git's global + system config at temp/empty files so
// the test never reads or writes the real ~/.gitconfig.
func IsolateGitConfig(t *testing.T) string {
	t.Helper()
	// Drop the repo-pointing GIT_* vars a git hook exports (GIT_DIR,
	// GIT_INDEX_FILE, …). Under the aphrollo tdd pre-commit gate they point at
	// the REAL repo; without this, fixture git ops would target (and can
	// corrupt) the real .git. Restored on cleanup.
	for _, k := range []string{
		"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE",
		"GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX",
	} {
		if v, ok := os.LookupEnv(k); ok {
			os.Unsetenv(k)
			t.Cleanup(func() { os.Setenv(k, v) })
		}
	}
	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	// CLAUDE_CONFIG_DIR is deliberately left alone here: Main already
	// isolates the whole package from the operator's real ~/.claude, and a
	// caller that also builds a git-hosting FIXTURE through this helper (
	// MakeCargoRepo/MakeGoRepo) may have its OWN CLAUDE_CONFIG_DIR already
	// set for a reason — to read gate.log back out of it later. Setting one
	// here unconditionally used to clobber that (#394 review): a workspace-
	// check test's own isolated state dir was silently swapped out from
	// under it, so its gate.log assertion failed against a directory that
	// was never written to. A test that specifically needs gate.log
	// isolated to ITSELF sets its own t.Setenv("CLAUDE_CONFIG_DIR", ...),
	// same as every test in behind_test.go already does.
	// Every caller of this helper points core.hooksPath at a hooks dir under
	// t.TempDir() while the global config it writes to is ALSO isolated here
	// — exactly the sanctioned dogfooding shape installGitGate's temp/
	// scratchpad refusal exists to let through. A test that wants to prove
	// the refusal itself clears this back off after calling in. A package
	// with no hooks install hands over no variable, and nothing in it reads
	// one.
	if active.HooksDirUnsafeEnv != "" {
		t.Setenv(active.HooksDirUnsafeEnv, "1")
	}
	return gc
}
