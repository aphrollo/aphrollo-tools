package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// --- pure-logic tests: no process spawning, no lock, fast -----------------

// TestCargoVerb_TableDriven pins the verb-detection rule task A9's split
// depends on: the first argv entry that is not a "-" option is the verb.
// Crucially, "nextest run" must resolve to "nextest" (nextest IS the verb;
// "run" is nextest's own sub-subcommand), never to "run".
func TestCargoVerb_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"plain run", []string{"run", "-p", "server"}, "run"},
		{"nextest run is its own verb, not run", []string{"nextest", "run", "-p", "server"}, "nextest"},
		{"test", []string{"test"}, "test"},
		{"global flag before the verb", []string{"-v", "run", "-p", "server"}, "run"},
		{"all flags, no verb", []string{"-v", "--locked"}, ""},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cargoVerb(c.args); got != c.want {
				t.Fatalf("cargoVerb(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// TestIsCargoRunVerb_ExcludesNextestRunAndTest pins the scope limit the
// coordinator called out explicitly: only bare `cargo run` takes the
// split-lock path. `nextest run` and `test` must NOT -- their own execution
// IS what the machine-wide lock exists to serialize.
func TestIsCargoRunVerb_ExcludesNextestRunAndTest(t *testing.T) {
	if isCargoRunVerb([]string{"nextest", "run", "-p", "server"}) {
		t.Fatal("`nextest run` must NOT be treated as `cargo run`")
	}
	if isCargoRunVerb([]string{"test"}) {
		t.Fatal("`cargo test` must NOT be treated as `cargo run`")
	}
	if !isCargoRunVerb([]string{"run", "-p", "server"}) {
		t.Fatal("`cargo run` must be recognized")
	}
}

// TestCargoRunArgsToBuildArgs_TableDriven pins the argv transformation:
// verb run -> build, everything from the first bare "--" onward dropped
// (the launched program's own args), everything else preserved verbatim.
func TestCargoRunArgsToBuildArgs_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			"simple run with program args stripped",
			[]string{"run", "-p", "server", "--", "arg1", "arg2"},
			[]string{"build", "-p", "server"},
		},
		{
			"no -- at all",
			[]string{"run", "--release", "-p", "server"},
			[]string{"build", "--release", "-p", "server"},
		},
		{
			"-- with nothing after it",
			[]string{"run", "--"},
			[]string{"build"},
		},
		{
			"global flag before the verb is preserved",
			[]string{"-v", "run", "-p", "server"},
			[]string{"-v", "build", "-p", "server"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cargoRunArgsToBuildArgs(c.args)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("cargoRunArgsToBuildArgs(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// --- realCargo stub for the split-lock integration tests -------------------

var (
	runVerbStubOnce sync.Once
	runVerbStubPath string
	runVerbStubErr  error
)

// runVerbStub returns the path to a tiny compiled Go binary standing in for
// "realCargo" in the split-lock tests below: it exits 0 unless
// APHROLLO_TEST_STUB_FAIL_ON names its own first argument (the verb it was
// invoked with -- "build" or "run"), in which case it exits 1. Unlike
// stubCargoArgsExit (used elsewhere in this package for tests where the
// caller fully controls a FIXED argv), these tests need a stand-in that
// accepts WHATEVER argv the shim actually constructs -- first the
// transformed build args, then the original run args -- so a fixed-argv
// cmd/sh stub cannot serve both calls. Built once per test binary run:
// compiling is not a sleep, and go's build cache makes a second identical
// build in the same process effectively free regardless.
func runVerbStub(t *testing.T) string {
	t.Helper()
	runVerbStubOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aphrollo-cargo-run-stub-")
		registerStubDir(dir)
		if err != nil {
			runVerbStubErr = err
			return
		}
		src := filepath.Join(dir, "stub.go")
		source := "package main\n\n" +
			"import \"os\"\n\n" +
			"func main() {\n" +
			// Record what the shim handed this child, so a test can prove the
			// slot token reaches the long phase instead of inspecting the
			// parent's own environment.
			"\tif out := os.Getenv(\"APHROLLO_TEST_STUB_ENV_OUT\"); out != \"\" && len(os.Args) > 1 {\n" +
			"\t\tos.WriteFile(out+\".\"+os.Args[1], []byte(os.Getenv(\"APHROLLO_SLOT_TOKEN\")), 0o600)\n" +
			"\t}\n" +
			"\tfailOn := os.Getenv(\"APHROLLO_TEST_STUB_FAIL_ON\")\n" +
			"\tif failOn != \"\" && len(os.Args) > 1 && os.Args[1] == failOn {\n" +
			"\t\tos.Exit(1)\n" +
			"\t}\n" +
			"\tos.Exit(0)\n" +
			"}\n"
		if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
			runVerbStubErr = err
			return
		}
		out := filepath.Join(dir, "stub")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, src)
		if combined, err := cmd.CombinedOutput(); err != nil {
			runVerbStubErr = fmt.Errorf("building the cargo-run test stub: %v: %s", err, combined)
			return
		}
		runVerbStubPath = out
	})
	if runVerbStubErr != nil {
		t.Fatalf("could not build the cargo-run-split test stub: %v", runVerbStubErr)
	}
	return runVerbStubPath
}

// --- integration: the lock's actual state during each phase ---------------

// TestRunCargoShim_CargoRun_BuildsUnderLockThenRunsLockFree is the literal
// required test: a `cargo run` invocation must record exactly two execCargo
// calls -- first "build ..." (verb swapped, the "--" suffix and everything
// after it stripped) while the machine-wide lock is HELD, then the
// ORIGINAL "run ..." args once the lock is FREE again. Lock state at each
// call is observed via execCargoHookForTest calling tdd.TryAcquireBuildLock
// itself (a failed try during the hook means something else -- this
// process, via runCargoShim -- currently holds it; a successful try means
// it's free, and is immediately released again so it doesn't interfere).
func TestRunCargoShim_CargoRun_BuildsUnderLockThenRunsLockFree(t *testing.T) {
	withIsolatedCargoLock(t)
	stub := runVerbStub(t)
	cfg := cargoShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    stub,
	}

	var calls [][]string
	var lockHeldDuringBuild, lockFreeDuringRun bool
	execCargoHookForTest = func(args []string) {
		calls = append(calls, append([]string{}, args...))
		switch {
		case len(args) > 0 && args[0] == "build":
			_, _, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
			lockHeldDuringBuild = !ok
		case len(args) > 0 && args[0] == "run":
			_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
			lockFreeDuringRun = ok
			if ok {
				release()
			}
		}
	}
	t.Cleanup(func() { execCargoHookForTest = nil })

	inputArgs := []string{"run", "-p", "server", "--", "arg1", "arg2"}
	var stdout, stderr bytes.Buffer
	code := runCargoShim(inputArgs, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0, stderr=%s", code, stderr.String())
	}

	if len(calls) != 2 {
		t.Fatalf("expected exactly 2 execCargo calls (build then run), got %d: %+v", len(calls), calls)
	}
	wantBuild := []string{"build", "-p", "server"}
	if !reflect.DeepEqual(calls[0], wantBuild) {
		t.Fatalf("first call = %+v, want %+v (verb swapped, -- suffix stripped)", calls[0], wantBuild)
	}
	if !reflect.DeepEqual(calls[1], inputArgs) {
		t.Fatalf("second call = %+v, want the ORIGINAL run args %+v unchanged", calls[1], inputArgs)
	}
	if !lockHeldDuringBuild {
		t.Fatal("expected the build lock to be HELD during the build phase")
	}
	if !lockFreeDuringRun {
		t.Fatal("expected the build lock to be FREE during the run phase")
	}
}

// TestRunCargoShim_CargoRun_FailingBuildNeverInvokesRun is the literal
// required test: a build that fails must propagate its exit code
// immediately, never invoke `run` at all, and must still release the lock
// (a failing build must not wedge the machine lock any more permanently
// than a successful one holding it forever would have).
func TestRunCargoShim_CargoRun_FailingBuildNeverInvokesRun(t *testing.T) {
	withIsolatedCargoLock(t)
	stub := runVerbStub(t)
	t.Setenv("APHROLLO_TEST_STUB_FAIL_ON", "build")

	cfg := cargoShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    stub,
	}

	var calls [][]string
	execCargoHookForTest = func(args []string) {
		calls = append(calls, append([]string{}, args...))
	}
	t.Cleanup(func() { execCargoHookForTest = nil })

	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"run", "-p", "server"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the build's own exit code)", code)
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 call (build only -- run must never be invoked after a failed build), got %d: %+v", len(calls), calls)
	}
	if calls[0][0] != "build" {
		t.Fatalf("the one call must be the build, got %+v", calls[0])
	}

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
	if !ok {
		t.Fatal("expected the build lock to be released after a failed build")
	}
	release()
}

// TestRunCargoShim_NonRunVerb_HoldsLockAcrossWholeCall is the literal
// required test: `cargo test` (any non-run verb) must NOT be split -- ONE
// execCargo call, with the lock HELD for its entirety -- and must still
// release the lock once it returns.
func TestRunCargoShim_NonRunVerb_HoldsLockAcrossWholeCall(t *testing.T) {
	withIsolatedCargoLock(t)
	stub := runVerbStub(t)
	cfg := cargoShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    stub,
	}

	var calls [][]string
	var lockHeldDuringCall bool
	execCargoHookForTest = func(args []string) {
		calls = append(calls, append([]string{}, args...))
		_, _, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
		lockHeldDuringCall = !ok
	}
	t.Cleanup(func() { execCargoHookForTest = nil })

	inputArgs := []string{"test", "-p", "server"}
	var stdout, stderr bytes.Buffer
	code := runCargoShim(inputArgs, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly ONE execCargo call for a non-run verb (no build/run split), got %d: %+v", len(calls), calls)
	}
	if !reflect.DeepEqual(calls[0], inputArgs) {
		t.Fatalf("the single call must be the ORIGINAL args unchanged, got %+v want %+v", calls[0], inputArgs)
	}
	if !lockHeldDuringCall {
		t.Fatal("expected the build lock to be HELD during a non-run verb's execution -- its own execution IS what's serialized")
	}

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
	if !ok {
		t.Fatal("expected the build lock to be released once the non-run call completes")
	}
	release()
}
