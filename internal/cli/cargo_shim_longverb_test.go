package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestIsCargoLongVerb_OnlyTheNonCompilingLongRunners pins WHICH verbs get
// the split: the ones whose long phase does not itself compile into the
// shared target dir. `cargo mutants` copies the tree to its own directory
// and then runs for HOURS — holding a slot for all of it starved every other
// build on the box. `nextest run`/`test` are deliberately excluded: their
// execution IS what the slots govern.
func TestIsCargoLongVerb_OnlyTheNonCompilingLongRunners(t *testing.T) {
	long := [][]string{
		{"mutants", "--in-diff", "diff.txt"},
		{"bench", "-p", "movement"},
		{"install", "cargo-nextest"},
	}
	for _, args := range long {
		if !isCargoLongVerb(args) {
			t.Errorf("cargo %s must take the split-slot path", strings.Join(args, " "))
		}
	}
	governed := [][]string{
		{"nextest", "run", "-p", "server"},
		{"test"},
		{"build"},
		{"check"},
		{"clippy"},
		{"run", "-p", "client"},
		// watch recompiles on every save for as long as it is open, so
		// "prewarm once, then unlocked forever" hands the box to a process
		// that never stops building.
		{"watch", "-x", "check"},
	}
	for _, args := range governed {
		if isCargoLongVerb(args) {
			t.Errorf("cargo %s compiles into the shared target — it must hold its slot throughout", strings.Join(args, " "))
		}
	}
}

// TestCargoPrewarmArgs_WarmsWhatTheVerbWillCompile pins what the slot is
// actually held FOR: mutants only needs the tree to typecheck before it forks
// off its own copies, bench needs the BENCH targets (warming --tests warmed
// the wrong thing and left the real compile unslotted), the rest need the
// test binaries.
func TestCargoPrewarmArgs_WarmsWhatTheVerbWillCompile(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"mutants", "--in-diff", "d"}, []string{"check", "--tests"}},
		{[]string{"bench"}, []string{"build", "--benches"}},
		{[]string{"install", "cargo-nextest"}, []string{"build", "--tests"}},
	}
	for _, c := range cases {
		if got := cargoPrewarmArgs(c.args); !reflect.DeepEqual(got, c.want) {
			t.Errorf("cargoPrewarmArgs(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// chdirCargoProject puts the test in a directory that LOOKS like a cargo
// project, since the shim only prewarms where a manifest exists (a `cargo
// install` fired from an unrelated directory has nothing to build).
func chdirCargoProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"p\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// TestRunCargoShim_LongVerb_PrewarmsUnderSlotThenRunsFree is the behaviour
// that matters: `cargo mutants` records exactly two execCargo calls — the
// prewarm while a slot is HELD, then the ORIGINAL args once the slot is
// free again — so a multi-hour run never owns the box's build capacity.
func TestRunCargoShim_LongVerb_PrewarmsUnderSlotThenRunsFree(t *testing.T) {
	withIsolatedCargoLock(t)
	// A gated run is the only `cargo mutants` that reaches this path at all:
	// a bare one is refused before the slots are ever consulted. The gated
	// run is recognised by building into the dedicated mutants worktree's own
	// target dir, not by any environment handshake.
	chdirCargoProject(t)
	t.Setenv("CARGO_TARGET_DIR", mutantsTargetDirForTest(t))
	cfg := cargoShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    runVerbStub(t),
	}

	var calls [][]string
	var slotHeldDuringPrewarm, slotFreeDuringLongRun bool
	execCargoHookForTest = func(args []string) {
		calls = append(calls, append([]string{}, args...))
		switch args[0] {
		case "check":
			_, _, ok := tdd.TryAcquireBuildSlot(shimTargetDir(), "cargo nextest run -p other-crate", "/some/other/repo")
			slotHeldDuringPrewarm = !ok
		case "mutants":
			_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir(), "cargo nextest run -p other-crate", "/some/other/repo")
			slotFreeDuringLongRun = ok
			if ok {
				release()
			}
		}
	}
	t.Cleanup(func() { execCargoHookForTest = nil })

	inputArgs := []string{"mutants", "--in-diff", "d.diff"}
	var stdout, stderr bytes.Buffer
	if code := runCargoShim(inputArgs, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0, stderr=%s", code, stderr.String())
	}

	if len(calls) != 2 {
		t.Fatalf("expected a prewarm then the long run, got %d calls: %+v", len(calls), calls)
	}
	if want := []string{"check", "--tests"}; !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("first call = %+v, want the prewarm %+v", calls[0], want)
	}
	if !reflect.DeepEqual(calls[1], inputArgs) {
		t.Fatalf("second call = %+v, want the ORIGINAL args %+v", calls[1], inputArgs)
	}
	if !slotHeldDuringPrewarm {
		t.Error("the slot must be HELD during the prewarm — that is the compile the governor is for")
	}
	if !slotFreeDuringLongRun {
		t.Error("the slot must be FREE during the long run — this is the starvation fix")
	}
}

// TestRunCargoShim_LongVerb_FailedPrewarmStillRuns pins that the prewarm is
// a warm-up, not a gate: whatever it makes of the tree, the verb the
// operator actually typed runs and its own exit code is what they get. (A
// `cargo run` build failure DOES abort — there the build is the thing being
// launched; here it is a courtesy compile.)
func TestRunCargoShim_LongVerb_FailedPrewarmStillRuns(t *testing.T) {
	withIsolatedCargoLock(t)
	chdirCargoProject(t)
	t.Setenv("APHROLLO_TEST_STUB_FAIL_ON", "build")
	cfg := cargoShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    runVerbStub(t),
	}

	var calls [][]string
	execCargoHookForTest = func(args []string) { calls = append(calls, append([]string{}, args...)) }
	t.Cleanup(func() { execCargoHookForTest = nil })

	var stdout, stderr bytes.Buffer
	if code := runCargoShim([]string{"bench", "-p", "movement"}, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("exit = %d, want the long verb's own 0 — a failed prewarm must not abort it", code)
	}
	if len(calls) != 2 || calls[1][0] != "bench" {
		t.Fatalf("expected the bench to run after the failed prewarm, got: %+v", calls)
	}

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir(), "cargo nextest run -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("the slot must be released even when the prewarm fails")
	}
	release()
}

// TestRunCargoShim_LongVerb_NoManifestSkipsThePrewarm pins the one case
// where there is nothing to warm: `cargo install <crate>` fired from a
// directory that is not a cargo project. Prewarming there would print a
// confusing "could not find Cargo.toml" for a command that is about to work
// perfectly well.
func TestRunCargoShim_LongVerb_NoManifestSkipsThePrewarm(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Chdir(t.TempDir())
	cfg := cargoShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    runVerbStub(t),
	}

	var calls [][]string
	execCargoHookForTest = func(args []string) { calls = append(calls, append([]string{}, args...)) }
	t.Cleanup(func() { execCargoHookForTest = nil })

	inputArgs := []string{"install", "cargo-nextest"}
	var stdout, stderr bytes.Buffer
	if code := runCargoShim(inputArgs, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], inputArgs) {
		t.Fatalf("expected exactly the one real call outside a cargo project, got: %+v", calls)
	}
}
