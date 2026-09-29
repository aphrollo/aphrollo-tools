package mutation

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// resetSrc is production code whose only guard stands between an empty
// directory and a reset of the process's own working directory: the shape
// issue #972 hit, where a mutant skipped an error check and an empty
// checkout path reached `git read-tree -u --reset`, which then ran wherever
// the test process stood.
const resetSrc = `package m

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// Reset puts dir's checkout back to its HEAD and drops its scratch notes.
func Reset(dir string) error {
	if dir == "" {
		return errors.New("no checkout to reset")
	}
	_ = os.Remove(filepath.Join(dir, "notes.txt"))
	_ = os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("rewritten by a mutant\n"), 0o644)
	cmd := exec.Command("git", "read-tree", "-u", "--reset", "HEAD")
	cmd.Dir = dir
	return cmd.Run()
}
`

const resetTestSrc = `package m

import "testing"

func TestReset_RefusesAnEmptyDir(t *testing.T) {
	if err := Reset(""); err == nil {
		t.Fatal("Reset ran with no checkout named")
	}
}
`

// resetMutation is the mutant #972 describes: the empty-path guard skipped.
var resetMutation = MutantsProveOptions{
	Old:      `if dir == "" {`,
	New:      `if dir == "never" {`,
	WantFail: "TestReset_RefusesAnEmptyDir",
}

// proveReset runs resetMutation against lane's reset.go.
func proveReset(lane string, run SuiteRunner, out, errb *bytes.Buffer) int {
	opts := resetMutation
	opts.File = filepath.Join(lane, "reset.go")
	return RunMutantsProve(opts, run, out, errb)
}

// laneWithWork builds a committed Go repository carrying the kinds of work a
// lane holds between commits: an uncommitted edit to a tracked file, a
// staged edit, and an untracked file nobody has added yet.
func laneWithWork(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n")
	write(t, root, "reset.go", resetSrc)
	write(t, root, "reset_test.go", resetTestSrc)
	write(t, root, "tracked.txt", "committed\n")
	write(t, root, "staged.txt", "committed\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	gitDo(t, root, "remote", "add", "origin", "https://example.invalid/lane.git")
	write(t, root, "staged.txt", "staged edit\n")
	gitDo(t, root, "add", "staged.txt")
	write(t, root, "tracked.txt", "uncommitted edit\n")
	write(t, root, "notes.txt", "untracked work\n")
	return root
}

func proveReadText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// assertLaneIntact checks every piece of work laneWithWork left in root.
func assertLaneIntact(t *testing.T, root string) {
	t.Helper()
	for rel, want := range map[string]string{
		"reset.go":    resetSrc,
		"tracked.txt": "uncommitted edit\n",
		"staged.txt":  "staged edit\n",
		"notes.txt":   "untracked work\n",
	} {
		if got, err := os.ReadFile(filepath.Join(root, rel)); err != nil || string(got) != want {
			t.Errorf("lane file %s = %q (err %v), want %q", rel, got, err, want)
		}
	}
	if staged := gitOutT(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "staged.txt" {
		t.Errorf("the lane's index lost its staged edit: git diff --cached names %q", staged)
	}
}

// assertNoSandboxLeft checks the area proofs make their copies in is empty.
func assertNoSandboxLeft(t *testing.T, lane string) {
	t.Helper()
	area := proveSandboxArea(lane)
	entries, err := os.ReadDir(area)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("a proof's copy was left behind: %s", filepath.Join(area, e.Name()))
	}
}

// Issue #972's regression: a mutant that writes and resets the directory it
// runs in must reach only the proof's own copy. The real runner is used, so
// the mutated code really executes, really runs git, and really deletes.
func TestRunMutantsProve_AMutantThatResetsItsCheckoutLeavesTheLaneIntact(t *testing.T) {
	lane := laneWithWork(t)

	var out, errb bytes.Buffer
	code := proveReset(lane, RunSuite(precommitTestTimeout), &out, &errb)

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveKilled, out.String(), errb.String())
	}
	assertLaneIntact(t, lane)
	assertNoSandboxLeft(t, lane)
}

// The copy the run sees holds the lane's current state, not its last commit:
// the mutation, the uncommitted edit, the staged edit and the untracked file
// are all there, and nothing in it leads back to the lane.
func TestRunMutantsProve_RunsInACopyHoldingTheLanesCurrentState(t *testing.T) {
	lane := laneWithWork(t)
	// Symlinks need a privilege a Windows account may lack; where one cannot
	// be made, the rest of the state is still checked.
	linked := os.Symlink("tracked.txt", filepath.Join(lane, "link.txt")) == nil
	branch := gitOutT(t, lane, "symbolic-ref", "HEAD")

	var seen string
	run := func(r Runner, root string) SuiteResult {
		seen = root
		if rel, err := filepath.Rel(lane, root); err == nil && filepath.IsLocal(rel) {
			t.Errorf("the run was handed a directory inside the lane: %s", root)
		}
		if got := proveReadText(t, filepath.Join(root, "reset.go")); !strings.Contains(got, resetMutation.New) {
			t.Errorf("the copy does not carry the mutation:\n%s", got)
		}
		if got := proveReadText(t, filepath.Join(root, "tracked.txt")); got != "uncommitted edit\n" {
			t.Errorf("the copy lost the lane's uncommitted edit: %q", got)
		}
		if got := proveReadText(t, filepath.Join(root, "notes.txt")); got != "untracked work\n" {
			t.Errorf("the copy lost the lane's untracked file: %q", got)
		}
		if staged := gitOutT(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "staged.txt" {
			t.Errorf("the copy's index does not carry the lane's staged edit: %q", staged)
		}
		if got := gitOutT(t, root, "symbolic-ref", "HEAD"); got != branch {
			t.Errorf("the copy's HEAD is %q, want the lane's branch %q", got, branch)
		}
		if linked {
			if got, err := os.Readlink(filepath.Join(root, "link.txt")); err != nil || got != "tracked.txt" {
				t.Errorf("the copy's link.txt points at %q (err %v), want the lane's tracked.txt", got, err)
			}
		}
		if remotes := gitOutT(t, root, "remote"); strings.TrimSpace(remotes) != "" {
			t.Errorf("the copy keeps a remote a mutant could push into: %q", remotes)
		}
		return SuiteResult{Passed: false, Output: "--- FAIL: TestReset_RefusesAnEmptyDir (0.00s)\nFAIL\n"}
	}

	var out, errb bytes.Buffer
	code := proveReset(lane, run, &out, &errb)

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveKilled, out.String(), errb.String())
	}
	if seen == "" {
		t.Fatal("the suite never ran")
	}
	if _, err := os.Stat(seen); !os.IsNotExist(err) {
		t.Errorf("the copy the proof ran in is still on disk: %s (stat err: %v)", seen, err)
	}
	assertLaneIntact(t, lane)
	assertNoSandboxLeft(t, lane)
}

// A lane on a detached HEAD gives the copy the same commit, detached.
func TestRunMutantsProve_ADetachedLanesCopyIsAtTheSameCommit(t *testing.T) {
	lane := laneWithWork(t)
	gitDo(t, lane, "checkout", "-q", "--detach")
	want := gitOutT(t, lane, "rev-parse", "HEAD")

	var got string
	run := func(r Runner, root string) SuiteResult {
		got = gitOutT(t, root, "rev-parse", "HEAD")
		return SuiteResult{Passed: false, Output: "--- FAIL: TestReset_RefusesAnEmptyDir (0.00s)\nFAIL\n"}
	}
	var out, errb bytes.Buffer
	if code := proveReset(lane, run, &out, &errb); code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveKilled, out.String(), errb.String())
	}
	if got != want {
		t.Fatalf("the copy's HEAD is %q, want the lane's detached %q", got, want)
	}
}

// A refusal reached after the copy exists removes it too.
func TestRunMutantsProve_ARefusalAfterTheCopyRemovesIt(t *testing.T) {
	lane := laneWithWork(t)
	// A clean filter that normalises the mutation away: the landing check
	// refuses after the copy is made and the mutation written into it.
	write(t, lane, ".gitattributes", "reset.go filter=same\n")
	gitDo(t, lane, "config", "filter.same.clean", "sed s/never//")

	ran := false
	var out, errb bytes.Buffer
	code := proveReset(lane, func(Runner, string) SuiteResult { ran = true; return SuiteResult{Passed: true} }, &out, &errb)

	if code != ExitMutantsProveRefused || ran {
		t.Fatalf("exit = %d (ran %v), want a refusal before the run\nstdout: %s\nstderr: %s",
			code, ran, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "unchanged") {
		t.Fatalf("refused for another reason than the landing check: %s", errb.String())
	}
	assertLaneIntact(t, lane)
	assertNoSandboxLeft(t, lane)
}

// A signal delivered mid-run removes the copy before the process exits, the
// way GatePRMerge's handler removes its throwaway checkout (#819).
func TestRunMutantsProve_ASignalRemovesTheCopyBeforeExit(t *testing.T) {
	lane := laneWithWork(t)

	exited := make(chan int, 1)
	origExit := proveSignalExit
	proveSignalExit = func(code int) { exited <- code }
	t.Cleanup(func() { proveSignalExit = origExit })
	injected := make(chan os.Signal, 1)
	origSource := proveSignalChan
	proveSignalChan = func() (chan os.Signal, func()) { return injected, func() {} }
	t.Cleanup(func() { proveSignalChan = origSource })

	// Only the first run is interrupted: a widened second run finds the
	// copy already gone and has nothing left to prove.
	var once sync.Once
	signalled := false
	run := func(r Runner, root string) SuiteResult {
		once.Do(func() {
			signalled = true
			injected <- syscall.SIGTERM
			select {
			case code := <-exited:
				if code != 128+int(syscall.SIGTERM) {
					t.Errorf("exit code after SIGTERM = %d, want %d", code, 128+int(syscall.SIGTERM))
				}
			case <-time.After(10 * time.Second):
				t.Fatal("a SIGTERM delivered mid-run never reached the handler")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Errorf("the copy was still on disk once the handler exited: %s (stat err: %v)", root, err)
			}
		})
		return SuiteResult{Passed: true}
	}

	var out, errb bytes.Buffer
	proveReset(lane, run, &out, &errb)

	if !signalled {
		t.Fatalf("the suite never ran\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	assertLaneIntact(t, lane)
	assertNoSandboxLeft(t, lane)
}

// proveEnvValue is the value env gives key, and whether it gives one at all.
func proveEnvValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// proveIsUnder reports whether path lies inside dir.
func proveIsUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}

// A cargo proof builds into one target directory beside the lane, the same
// one proof after proof: never the copy's own, which is cold every time and
// deleted with it, and never the lane's, which is inside the lane.
func TestRunMutantsProve_ACargoProofBuildsInAStableTargetOutsideTheLane(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	lane := tddtest.MakeCargoRepo(t)

	var targets []string
	run := func(r Runner, root string) SuiteResult {
		target, ok := proveEnvValue(r.Env, "CARGO_TARGET_DIR")
		if !ok {
			t.Errorf("the cargo run carries no CARGO_TARGET_DIR: %q", r.Env)
		}
		if proveIsUnder(target, lane) || proveIsUnder(target, root) {
			t.Errorf("CARGO_TARGET_DIR %s lies inside the lane %s or the copy %s", target, lane, root)
		}
		targets = append(targets, target)
		return SuiteResult{Passed: false, Output: "test tests::base_is_zero ... FAILED\n"}
	}
	for range 2 {
		var out, errb bytes.Buffer
		code := RunMutantsProve(MutantsProveOptions{
			File: filepath.Join(lane, "src", "lib.rs"),
			Old:  "{ 0 }", New: "{ 1 }",
			WantFail: "base_is_zero",
		}, run, &out, &errb)
		if code != ExitMutantsProveKilled {
			t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
				code, ExitMutantsProveKilled, out.String(), errb.String())
		}
	}
	if len(targets) != 2 || targets[0] != targets[1] {
		t.Fatalf("two proofs of one lane built into %q, want one directory both times", targets)
	}
}

// A CARGO_TARGET_DIR the caller already set is where the bytes land, so the
// proof leaves it as it is.
func TestRunMutantsProve_ACargoProofKeepsATargetDirTheCallerSet(t *testing.T) {
	own := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", own)
	lane := tddtest.MakeCargoRepo(t)

	run := func(r Runner, root string) SuiteResult {
		if target, ok := proveEnvValue(r.Env, "CARGO_TARGET_DIR"); ok {
			t.Errorf("the proof overrode the caller's CARGO_TARGET_DIR %s with %s", own, target)
		}
		return SuiteResult{Passed: false, Output: "test tests::base_is_zero ... FAILED\n"}
	}
	var out, errb bytes.Buffer
	RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(lane, "src", "lib.rs"),
		Old:  "{ 0 }", New: "{ 1 }",
		WantFail: "base_is_zero",
	}, run, &out, &errb)
}

// Installed dependencies are gitignored, so the copy of the tracked tree has
// none; the lane's node_modules, the project's own and the monorepo root's,
// are linked in, and removing the copy unlinks them without reaching the
// installs themselves.
func TestRunMutantsProve_ANodeProofSeesTheLanesInstalledModules(t *testing.T) {
	lane := t.TempDir()
	gitInit(t, lane)
	write(t, lane, ".gitignore", "node_modules/\n")
	write(t, lane, "package.json", "{\"name\":\"root\"}\n")
	write(t, lane, "web/package.json", "{\"name\":\"web\",\"scripts\":{\"test\":\"vitest run\"}}\n")
	write(t, lane, "web/sum.js", "export const sum = (a, b) => a + b\n")
	gitDo(t, lane, "add", ".")
	gitDo(t, lane, "commit", "-qm", "base")
	write(t, lane, "node_modules/shared/index.js", "root install\n")
	write(t, lane, "web/node_modules/vitest/index.js", "web install\n")

	ran := false
	run := func(r Runner, root string) SuiteResult {
		ran = true
		for rel, want := range map[string]string{
			"node_modules/shared/index.js":     "root install\n",
			"web/node_modules/vitest/index.js": "web install\n",
		} {
			// root is the project root in the copy, web/; the copy of the
			// lane is its parent.
			base := filepath.Dir(root)
			if got := proveReadText(t, filepath.Join(base, filepath.FromSlash(rel))); got != want {
				t.Errorf("the copy's %s = %q, want the lane's %q", rel, got, want)
			}
		}
		return SuiteResult{Passed: false, Output: "FAIL web/sum.test.js > sum adds\n"}
	}
	var out, errb bytes.Buffer
	RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(lane, "web", "sum.js"),
		Old:  "a + b", New: "a - b",
		WantFail: "sum adds",
	}, run, &out, &errb)

	if !ran {
		t.Fatalf("the suite never ran\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	if got := proveReadText(t, filepath.Join(lane, "node_modules", "shared", "index.js")); got != "root install\n" {
		t.Errorf("the lane's root install changed: %q", got)
	}
	if got := proveReadText(t, filepath.Join(lane, "web", "node_modules", "vitest", "index.js")); got != "web install\n" {
		t.Errorf("the lane's web install changed: %q", got)
	}
	assertNoSandboxLeft(t, lane)
}

// proveRootsOf runs n proofs of lane and answers the directory each run was
// handed.
func proveRootsOf(t *testing.T, lane string, n int) []string {
	t.Helper()
	var roots []string
	run := func(r Runner, root string) SuiteResult {
		roots = append(roots, root)
		return SuiteResult{Passed: false, Output: "--- FAIL: TestReset_RefusesAnEmptyDir (0.00s)\nFAIL\n"}
	}
	for range n {
		var out, errb bytes.Buffer
		if code := proveReset(lane, run, &out, &errb); code != ExitMutantsProveKilled {
			t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
				code, ExitMutantsProveKilled, out.String(), errb.String())
		}
	}
	return roots
}

// The go build cache keys a compile on the directory it ran in, so a copy at
// a new path every time rebuilds everything the tested package imports:
// measured at 1.8 s of a 2.5 s proof. Proofs of one lane reuse one path.
func TestRunMutantsProve_ProofsOfOneLaneRunAtOnePath(t *testing.T) {
	lane := laneWithWork(t)

	roots := proveRootsOf(t, lane, 2)

	if len(roots) != 2 || roots[0] != roots[1] {
		t.Fatalf("two proofs of one lane ran in %q, want one path both times", roots)
	}
	assertNoSandboxLeft(t, lane)
}

// plantSlot leaves a copy in lane's reusable slot, held by pid, with a file
// in it the next proof must either leave alone or reclaim.
func plantSlot(t *testing.T, lane string, pid int) (slot, marker string) {
	t.Helper()
	slot = filepath.Join(proveSandboxArea(lane), proveSandboxSlot)
	marker = filepath.Join(slot, "in-use")
	write(t, slot, "in-use", "another proof's copy\n")
	write(t, slot, proveSandboxHolder, "pid="+strconv.Itoa(pid)+"\n")
	return slot, marker
}

// A copy in the slot whose holder is alive is another proof's, running now:
// this proof takes a directory of its own and never touches that one.
func TestRunMutantsProve_ALiveProofsCopyIsLeftAlone(t *testing.T) {
	lane := laneWithWork(t)
	slot, marker := plantSlot(t, lane, os.Getpid())
	t.Cleanup(func() { _ = os.RemoveAll(slot) })

	roots := proveRootsOf(t, lane, 1)

	if len(roots) != 1 || proveIsUnder(roots[0], slot) || !proveIsUnder(roots[0], proveSandboxArea(lane)) {
		t.Fatalf("the proof ran in %q, want a directory of its own in %s beside the live proof's copy %s",
			roots, proveSandboxArea(lane), slot)
	}
	if got := proveReadText(t, marker); got != "another proof's copy\n" {
		t.Fatalf("the live proof's copy changed: %q", got)
	}
}

// A copy in the slot whose holder is gone is a killed proof's leftover: the
// next proof reclaims the slot rather than paying a cold build forever.
func TestRunMutantsProve_ADeadProofsCopyIsReclaimed(t *testing.T) {
	lane := laneWithWork(t)
	slot, marker := plantSlot(t, lane, 4194301)
	t.Cleanup(SetPidRunningForTest(func(pid int) bool { return pid != 4194301 }))

	roots := proveRootsOf(t, lane, 1)

	if len(roots) != 1 || !proveIsUnder(roots[0], slot) {
		t.Fatalf("the proof ran in %q, not in the reclaimed slot %s", roots, slot)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the dead proof's leftover is still there: %s (stat err: %v)", marker, err)
	}
	assertNoSandboxLeft(t, lane)
}

// A copy that cannot be made refuses the proof before anything is written or
// run: no copy means no place to run a mutant but the lane.
func TestRunMutantsProve_ACopyThatCannotBeMadeRefusesBeforeRunning(t *testing.T) {
	lane := laneWithWork(t)
	// A file where the area's parent directory belongs.
	write(t, filepath.Dir(lane), ".mutants", "not a directory\n")

	ran := false
	var out, errb bytes.Buffer
	code := proveReset(lane, func(Runner, string) SuiteResult { ran = true; return SuiteResult{Passed: true} }, &out, &errb)

	if code != ExitMutantsProveRefused || ran {
		t.Fatalf("exit = %d (ran %v), want a refusal before any run\nstdout: %s\nstderr: %s",
			code, ran, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "lane was not touched") {
		t.Fatalf("the refusal does not say the lane was left alone: %s", errb.String())
	}
	assertLaneIntact(t, lane)
}

// A red run on another test than the predicted one is a wrong failure, and
// its copy goes the same way as a kill's.
func TestRunMutantsProve_AWrongFailureLeavesTheLaneIntact(t *testing.T) {
	lane := laneWithWork(t)

	var out, errb bytes.Buffer
	code := proveReset(lane, func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: "--- FAIL: TestSomethingElse_Broke (0.00s)\nFAIL\n"}
	}, &out, &errb)

	if code != ExitMutantsProveWrongFailure {
		t.Fatalf("exit = %d, want ExitMutantsProveWrongFailure (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveWrongFailure, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "TestSomethingElse_Broke") {
		t.Fatalf("the report does not name the test that did fail: %s", out.String())
	}
	assertLaneIntact(t, lane)
	assertNoSandboxLeft(t, lane)
}
