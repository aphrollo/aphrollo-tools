package tdd

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
	// identity config that gitInit used to spawn four processes for.
	initFixture string
	// goFixture and cargoFixture add the committed base each helper's
	// callers expect, and with it the runner marker (go.mod / Cargo.toml)
	// that DetectRunner reads.
	goFixture    string
	cargoFixture string
)

// buildFixtures builds the golden repos under dir. It runs from TestMain, so
// it takes no *testing.T and panics rather than failing a test: a package
// whose fixtures cannot be built has no test that can pass.
func buildFixtures(dir string) {
	// Same isolation gitInit gives each test, applied once for the build:
	// the operator's global config carries core.hooksPath (the gate), a
	// signing key and an init.defaultBranch, none of which a fixture wants.
	// Restored before m.Run so per-test isolation is unchanged.
	restore := isolateGitConfigEnv(filepath.Join(dir, "fixture-gitconfig"))
	defer restore()

	initFixture = filepath.Join(dir, "fixture-init")
	mustInitRepo(initFixture)

	goFixture = filepath.Join(dir, "fixture-go")
	mustCopyDir(goFixture, initFixture)
	mustWriteFile(filepath.Join(goFixture, "go.mod"), "module example.com/m\n\ngo 1.26\n")
	mustWriteFile(filepath.Join(goFixture, "doc.go"), "package m\n")
	mustGit(goFixture, "add", ".")
	mustGit(goFixture, "commit", "-qm", "base")

	cargoFixture = filepath.Join(dir, "fixture-cargo")
	mustCopyDir(cargoFixture, initFixture)
	mustWriteFile(filepath.Join(cargoFixture, "Cargo.toml"), "[package]\nname = \"m\"\nversion = \"0.1.0\"\n")
	mustWriteFile(filepath.Join(cargoFixture, "src", "lib.rs"), "pub fn base() -> i32 { 0 }\n")
	mustGit(cargoFixture, "add", ".")
	mustGit(cargoFixture, "commit", "-qm", "base")
}

// isolateGitConfigEnv is isolateGitConfig without a *testing.T: it drops the
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
	mustBeUnderTemp(dir)
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
	cmd := exec.Command(gitBinary(), args...)
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

// mustCopyDir copies a built fixture into a fresh directory. dst must not hold
// any of src's files yet — os.CopyFS refuses to overwrite, which is the check
// that a fixture is never handed out twice.
func mustCopyDir(dst, src string) {
	mustBeUnderTemp(dst)
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		panic(err)
	}
}

// A fixture repository is only ever built in a temp dir, and the helper is
// where that is enforced rather than assumed. Under mutation a production
// path that resolves a repo root to "" lets git fall back to the process's
// own working directory — the package's own checkout — and the fixture's
// init/commit/merge then land in the real repository (issue #156). An empty
// or non-temp target is a defect in the caller, so the helper refuses it.
func TestFixtureTarget_RefusesADirectoryOutsideTheOSTempDir(t *testing.T) {
	for _, dst := range []string{"", RepoRoot(".")} {
		if err := fixtureTargetUnderTemp(dst); err == nil {
			t.Errorf("fixtureTargetUnderTemp(%q) = nil, want a refusal — a fixture must never be built there", dst)
		}
	}
}

// The predicate is worth nothing unless the helper CALLS it, so this goes
// through copyFixture itself: deleting the guard from it would leave the
// fixture copied into the repository, which is the write escape 156 was.
func TestCopyFixture_RefusesToBuildAFixtureOutsideTheTempDir(t *testing.T) {
	var refused error
	prev := fixtureRefused
	fixtureRefused = func(_ *testing.T, err error) { refused = err }
	t.Cleanup(func() { fixtureRefused = prev })

	got := copyFixture(t, filepath.Join(RepoRoot("."), "internal"), goFixture)

	if refused == nil {
		t.Fatal("copyFixture copied a golden repo into the repository's own tree")
	}
	if got != "" {
		t.Errorf("copyFixture = %q, want \"\" — a refused fixture has no directory to hand back", got)
	}
}

// The TestMain path has no *testing.T to fail, so it panics — and it is the
// path that builds the golden repos every other fixture is copied from.
func TestFixtureBuilders_PanicOnATargetOutsideTheTempDir(t *testing.T) {
	outside := filepath.Join(RepoRoot("."), "internal")
	for name, build := range map[string]func(){
		"mustInitRepo": func() { mustInitRepo(outside) },
		"mustCopyDir":  func() { mustCopyDir(outside, goFixture) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s(%q) returned — a golden repo must never be built in the repository", name, outside)
				}
			}()
			build()
		}()
	}
}

// The refusal is narrow: the directory every fixture actually uses passes.
func TestFixtureTarget_AcceptsATestTempDir(t *testing.T) {
	if err := fixtureTargetUnderTemp(t.TempDir()); err != nil {
		t.Errorf("a t.TempDir() target must be accepted, got %v", err)
	}
}

// fixtureTargetUnderTemp reports why dst is not a place a fixture repository
// may be built: nowhere at all (git would use the process's own working
// directory), or anywhere outside the OS temp dir.
func fixtureTargetUnderTemp(dst string) error {
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

// fixtureRefused is what a refused target does: fail the test that asked for
// it. A var so the guard's OWN test can observe the refusal instead of being
// killed by it — the guard is only real if it is reached through copyFixture.
var fixtureRefused = func(t *testing.T, err error) { t.Fatal(err) }

// mustBeUnderTemp is the same rule on the TestMain path, which has no
// *testing.T to fail: buildFixtures runs before any test exists, and a golden
// repo built in the repository is the write this rule is about.
func mustBeUnderTemp(dir string) {
	if err := fixtureTargetUnderTemp(dir); err != nil {
		panic(err)
	}
}

// copyFixture hands a test its own copy of one of the golden repos.
func copyFixture(t *testing.T, dst, src string) string {
	t.Helper()
	if err := fixtureTargetUnderTemp(dst); err != nil {
		fixtureRefused(t, err)
		return ""
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dst
}

// The fixture helpers spawn git for their own setup, and a session puts the
// queue shim dir FIRST on PATH. The shim re-enters aphrollo, which judges the
// fixture repo by the rules it holds the OPERATOR's checkout to: three tests
// went red on a box with the shim installed, refused with "primary checkout is
// merge-only" for a `git checkout -b` inside a temp-dir fixture. The package
// already resolves the real git for its own subprocesses (gitBinary); the
// fixtures owe the same answer, and get back the ~35 ms per spawn the shim
// costs on the way through.
func TestFixtureGit_RunsTheRealGitNotTheQueueShim(t *testing.T) {
	root := makeGoRepo(t)
	dir, marker := fakeGitShim(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	gitDo(t, root, "checkout", "-q", "-b", "lane")

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the fixture ran the queue shim instead of git")
	}
	if got := gitValue(t, root, "rev-parse", "--abbrev-ref", "HEAD"); got != "lane" {
		t.Fatalf("branch = %q, want lane — the fixture's git did not take", got)
	}
}

// The copy carries an index that was stat'd in a different directory. git
// compares content when the stat cache misses, so the copy still reads clean
// — and it has to: every staged-file assertion in this package starts from a
// fixture that git calls unmodified.
func TestMakeGoRepo_CopiedFixtureIsCleanWithOneCommit(t *testing.T) {
	root := makeGoRepo(t)

	if out := gitValue(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("a fresh fixture must be clean, got %q", out)
	}
	if n := gitValue(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("fixture has %s commits, want the single base commit", n)
	}
}

// Each caller gets its OWN repo. A shared one would make the 178 tests that
// commit into it one history, and the order they ran in would decide what
// each of them saw.
func TestMakeGoRepo_IsIndependentPerCall(t *testing.T) {
	a, b := makeGoRepo(t), makeGoRepo(t)
	if a == b {
		t.Fatalf("two fixtures share a directory: %s", a)
	}

	write(t, a, "second.go", "package m\n")
	gitDo(t, a, "add", ".")
	gitDo(t, a, "commit", "-qm", "second")

	if gitValue(t, a, "rev-parse", "HEAD") == gitValue(t, b, "rev-parse", "HEAD") {
		t.Fatal("a commit in one fixture repo reached another")
	}
	if out := gitValue(t, b, "status", "--porcelain"); out != "" {
		t.Fatalf("the untouched fixture must stay clean, got %q", out)
	}
}
