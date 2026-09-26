package merge

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The throwaway checkout GatePRMerge builds is a fresh `git worktree add`:
// it carries no gitignored node_modules, so an npm root's vitest or jest
// died at startup with ERR_MODULE_NOT_FOUND and every PR touching a frontend
// was refused on a green CI (issue #909). These tests build an npm root whose
// suite needs a package from node_modules, and never touch the network: the
// "package" is a directory written by hand, and `npm ci` is a fake answered
// through the SuiteRunner seam.

const (
	npmLock       = `{"lockfileVersion":3,"packages":{"node_modules/fakepkg":{"version":"1.0.0"}}}`
	npmLockBumped = `{"lockfileVersion":3,"packages":{"node_modules/fakepkg":{"version":"1.1.0"}}}`
	notFound      = "Error [ERR_MODULE_NOT_FOUND]: Cannot find package 'fakepkg'"
)

// installFakePkg writes the one package the fixture's suite requires.
func installFakePkg(t *testing.T, root string) {
	t.Helper()
	write(t, root, "node_modules/fakepkg/index.js", "module.exports = 1\n")
}

// npmPRGateLane is a lane whose change lies in the npm root frontend/, with
// trunk moved on since the fork — by a lockfile bump when bumpLock, else by an
// unrelated file. The lane has installed frontend/node_modules, as a lane a
// developer works in always has. A ratchet law pulls the merged-tree
// judgment in; it scopes nothing the fixture touches.
func npmPRGateLane(t *testing.T, bumpLock bool) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	writeCrateSizeLaw(t, root, 15)
	write(t, root, ".gitignore", "node_modules/\n")
	write(t, root, "frontend/package.json", `{"name":"fe","scripts":{"test":"node sum.test.js"},"devDependencies":{"fakepkg":"1.0.0"}}`)
	write(t, root, "frontend/package-lock.json", npmLock)
	write(t, root, "frontend/sum.js", "module.exports = (a, b) => a + b\n")
	write(t, root, "frontend/sum.test.js", "require('fakepkg')\nrequire('./sum')\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	trunk := currentBranch(t, root)

	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "frontend/sum.js", "module.exports = (a, b) => b + a\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane changes the npm root")

	gitDo(t, root, "checkout", "-q", trunk)
	if bumpLock {
		write(t, root, "frontend/package-lock.json", npmLockBumped)
	} else {
		write(t, root, "NOTES.txt", "trunk moves on\n")
	}
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk moves on")
	gitDo(t, root, "checkout", "-q", "lane")
	installFakePkg(t, filepath.Join(root, "frontend"))
	return root
}

// npmRun is one call the fake runner answered, and what node_modules was in
// the directory it ran in at that moment.
type npmRun struct {
	Runner Runner
	Dir    string
	// Linked is true when node_modules there was a link rather than a
	// directory of its own.
	Linked bool
}

func (r npmRun) isInstall() bool {
	return r.Runner.Cmd == "npm" && len(r.Runner.Args) == 1 && r.Runner.Args[0] == "ci"
}

// npmFakeRun is the suite runner the npm fixtures run under: `npm ci` is a
// fake npm answering install and, on a pass, writing the pinned package;
// every other command in an npm root stands in for node and fails as node
// does when fakepkg does not resolve from there.
func npmFakeRun(t *testing.T, seen *[]npmRun, install SuiteResult) SuiteRunner {
	return func(r Runner, dir string) SuiteResult {
		d := dir
		if r.Dir != "" {
			d = r.Dir
		}
		call := npmRun{Runner: r, Dir: d}
		if fi, err := os.Lstat(filepath.Join(d, "node_modules")); err == nil {
			call.Linked = fi.Mode().Type() != os.ModeDir
		}
		*seen = append(*seen, call)
		if call.isInstall() {
			if install.Passed {
				installFakePkg(t, d)
			}
			return install
		}
		if _, err := os.Stat(filepath.Join(d, "package.json")); err != nil {
			return SuiteResult{Passed: true}
		}
		if _, err := os.Stat(filepath.Join(d, "node_modules", "fakepkg", "index.js")); err != nil {
			return SuiteResult{Output: notFound}
		}
		return SuiteResult{Passed: true}
	}
}

// suiteRunsIn is every non-install call made in an npm root other than the lane's.
func suiteRunsIn(t *testing.T, seen []npmRun, lane string) []npmRun {
	t.Helper()
	var out []npmRun
	for _, r := range seen {
		if r.isInstall() || underDir(t, r.Dir, lane) {
			continue
		}
		if _, err := os.Stat(filepath.Join(r.Dir, "package.json")); err == nil || filepath.Base(r.Dir) == "frontend" {
			out = append(out, r)
		}
	}
	return out
}

func requireLaneNodeModulesIntact(t *testing.T, lane string) {
	t.Helper()
	p := filepath.Join(lane, "frontend", "node_modules", "fakepkg", "index.js")
	fi, err := os.Lstat(filepath.Join(lane, "frontend", "node_modules"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("the lane's own node_modules is no longer a directory (lstat err: %v)", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("the merge gate deleted into the lane's node_modules: %v", err)
	}
}

// The cheap path: trunk did not touch the lockfile, so the lane's installed
// node_modules is exactly what the merged tree pins. It is linked, not
// reinstalled, and the link goes with the checkout while the lane keeps
// every file.
func TestGatePRMerge_NpmRootLinksTheLanesNodeModulesWhenTheLockfileIsUnchanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := npmPRGateLane(t, false)

	var seen []npmRun
	if err := GatePRMerge(lane, npmFakeRun(t, &seen, SuiteResult{Passed: true}), io.Discard); err != nil {
		t.Fatalf("a green npm merge with an unchanged lockfile must land: %v", err)
	}
	for _, r := range seen {
		if r.isInstall() {
			t.Fatalf("installed although the lane's lockfile matches the merged tree: %+v", r)
		}
	}
	runs := suiteRunsIn(t, seen, lane)
	if len(runs) == 0 {
		t.Fatalf("no suite ran in the merged checkout's npm root: %+v", seen)
	}
	for _, r := range runs {
		if !r.Linked {
			t.Fatalf("the suite in %s ran without the lane's node_modules linked in: %+v", r.Dir, r)
		}
	}
	requireLaneNodeModulesIntact(t, lane)
	requireNoGatePRMergeCheckout(t, filepath.Join(filepath.Dir(lane), ".worktrees", filepath.Base(lane)))
}

// Trunk bumped the lockfile: the lane's install is of a different set, so the
// merged checkout installs its own, there and not in the lane.
func TestGatePRMerge_NpmRootInstallsWhenTheMergedLockfileDiffers(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := npmPRGateLane(t, true)

	var seen []npmRun
	if err := GatePRMerge(lane, npmFakeRun(t, &seen, SuiteResult{Passed: true}), io.Discard); err != nil {
		t.Fatalf("a green npm merge whose lockfile moved must land after installing: %v", err)
	}
	var installs []npmRun
	for _, r := range seen {
		if r.isInstall() {
			installs = append(installs, r)
		}
	}
	if len(installs) != 1 {
		t.Fatalf("ran %d install(s), want exactly one: %+v", len(installs), seen)
	}
	if underDir(t, installs[0].Dir, lane) || filepath.Base(installs[0].Dir) != "frontend" {
		t.Fatalf("installed in %s, want the merged checkout's frontend root", installs[0].Dir)
	}
	runs := suiteRunsIn(t, seen, lane)
	if len(runs) == 0 {
		t.Fatalf("no suite ran in the merged checkout's npm root: %+v", seen)
	}
	for _, r := range runs {
		if r.Linked {
			t.Fatalf("linked the lane's node_modules although its lockfile differs: %+v", r)
		}
	}
	requireLaneNodeModulesIntact(t, lane)
}

// An install that fails measured nothing: the merge is refused naming the
// root and what npm said, and no suite runs to turn it into a test failure.
func TestGatePRMerge_NpmInstallFailureRefusesNamingTheRootAndTheError(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := npmPRGateLane(t, true)

	var seen []npmRun
	err := GatePRMerge(lane, npmFakeRun(t, &seen, SuiteResult{Output: "npm ERR! 404 Not Found - fakepkg@1.1.0", Err: "exit status 1"}), io.Discard)

	if err == nil {
		t.Fatal("a failed install must refuse the merge")
	}
	for _, want := range []string{"frontend", "npm ci", "exit status 1", "npm ERR! 404 Not Found - fakepkg@1.1.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "ERR_MODULE_NOT_FOUND") {
		t.Errorf("a failed install was reported as a test failure: %v", err)
	}
	if runs := suiteRunsIn(t, seen, lane); len(runs) != 0 {
		t.Errorf("a suite ran after its root's install failed: %+v", runs)
	}
	requireNoGatePRMergeCheckout(t, filepath.Join(filepath.Dir(lane), ".worktrees", filepath.Base(lane)))
}

// The gate's own timeout bounds the install; one that runs out is refused as
// a timeout, never waved through as an install that happened.
func TestGatePRMerge_NpmInstallTimeoutRefusesAsATimeout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := npmPRGateLane(t, true)

	var seen []npmRun
	err := GatePRMerge(lane, npmFakeRun(t, &seen, SuiteResult{TimedOut: true, Output: "fetching fakepkg"}), io.Discard)

	if err == nil {
		t.Fatal("an install that timed out must refuse the merge")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("the refusal must say the install timed out, got: %v", err)
	}
	if runs := suiteRunsIn(t, seen, lane); len(runs) != 0 {
		t.Errorf("a suite ran after its root's install timed out: %+v", runs)
	}
}

// Only npm roots the merge's suites touch are provisioned: a lockfile trunk
// moved in an npm root this lane never touched costs no install, and the Go
// root the lane did touch has no node_modules to provision.
func TestGatePRMerge_NpmRootOutsideTheSuiteSetIsNotProvisioned(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	writeCrateSizeLaw(t, root, 15)
	write(t, root, ".gitignore", "node_modules/\n")
	write(t, root, "frontend/package.json", `{"name":"fe"}`)
	write(t, root, "frontend/package-lock.json", npmLock)
	write(t, root, "tool/go.mod", "module tool\n\ngo 1.22\n")
	write(t, root, "tool/main.go", "package main\n\nfunc main() {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	trunk := currentBranch(t, root)
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "tool/main.go", "package main\n\nfunc main() { println() }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane changes the go tool only")
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "frontend/package-lock.json", npmLockBumped)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk bumps the frontend lockfile")
	gitDo(t, root, "checkout", "-q", "lane")

	var seen []npmRun
	_ = GatePRMerge(root, npmFakeRun(t, &seen, SuiteResult{Passed: true}), io.Discard)
	for _, r := range seen {
		if r.isInstall() {
			t.Fatalf("installed an npm root no suite of this merge runs in: %+v", r)
		}
		if r.Runner.Cmd == "go" && strings.Join(r.Runner.Args, " ") == "mod download" {
			t.Fatalf("provisioned a go root, which has no node_modules to install: %+v", r)
		}
	}
}

// A signal caught mid-suite runs the same cleanup as a normal return: the
// link goes with the checkout, and the lane's node_modules behind it is
// untouched.
func TestGatePRMerge_KilledMidNpmSuiteRemovesTheLinkNotTheLanesNodeModules(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := npmPRGateLane(t, false)

	cleaned := make(chan struct{})
	origExit := prGateSignalExit
	prGateSignalExit = func(int) { close(cleaned) }
	t.Cleanup(func() { prGateSignalExit = origExit })
	injected := make(chan os.Signal, 1)
	origSource := prGateSignalChan
	prGateSignalChan = func() (chan os.Signal, func()) { return injected, func() {} }
	t.Cleanup(func() { prGateSignalChan = origSource })

	var seen []npmRun
	fake := npmFakeRun(t, &seen, SuiteResult{Passed: true})
	var once sync.Once
	var linkedDir string
	run := func(r Runner, dir string) SuiteResult {
		res := fake(r, dir)
		last := seen[len(seen)-1]
		if !last.Linked {
			return res
		}
		once.Do(func() {
			linkedDir = last.Dir
			injected <- syscall.SIGTERM
			select {
			case <-cleaned:
			case <-time.After(5 * time.Second):
				t.Fatal("a SIGTERM delivered mid-suite never reached the signal handler")
			}
			if _, err := os.Lstat(filepath.Join(last.Dir, "node_modules")); !os.IsNotExist(err) {
				t.Errorf("the link outlived the signal handler's cleanup (lstat err: %v)", err)
			}
			requireLaneNodeModulesIntact(t, lane)
		})
		return res
	}

	_ = GatePRMerge(lane, run, io.Discard)
	if linkedDir == "" {
		t.Fatalf("no suite ran with the lane's node_modules linked in: %+v", seen)
	}
	requireLaneNodeModulesIntact(t, lane)
}

// followingRemove deletes dir's contents the way a remover that follows links
// would: into a symlink's or junction's target. Git and os.RemoveAll do not do
// that on Linux; on Windows a junction is exactly where such a remover walks.
func followingRemove(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			followingRemove(p)
		}
		_ = os.Remove(p)
	}
}

// The links are removed as links BEFORE the checkout itself is: even a
// checkout remover that follows links into their targets then finds none, and
// the lane's node_modules keeps every file.
func TestGatePRMerge_CheckoutRemovalNeverReachesThroughTheLink(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := npmPRGateLane(t, false)
	orig := prGateRemoveCheckout
	prGateRemoveCheckout = func(laneWorktree, wt string) {
		followingRemove(wt)
		orig(laneWorktree, wt)
	}
	t.Cleanup(func() { prGateRemoveCheckout = orig })

	var seen []npmRun
	if err := GatePRMerge(lane, npmFakeRun(t, &seen, SuiteResult{Passed: true}), io.Discard); err != nil {
		t.Fatalf("a green npm merge with an unchanged lockfile must land: %v", err)
	}
	linked := false
	for _, r := range suiteRunsIn(t, seen, lane) {
		linked = linked || r.Linked
	}
	if !linked {
		t.Fatalf("no suite ran with the lane's node_modules linked in: %+v", seen)
	}
	requireLaneNodeModulesIntact(t, lane)
}
