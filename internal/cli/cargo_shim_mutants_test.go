package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ratchet: test_removed TestRunCargoShim_AllowsCargoMutantsThroughTheDeprecatedAliasAndLogsIt: there is no deprecated alias any more — the marker it named IS the handshake, so a call carrying it is the ordinary gated run TestCargoShim_AllowsMutantsWhenTheGateMarkedTheRun covers, not a graced one to count
// ratchet: test_removed TestRunCargoShim_AllowsCargoMutantsThroughTheGatesRunner: the shim no longer tells a gated run apart by the target dir it builds into; TestCargoShim_AllowsMutantsWhenTheGateMarkedTheRun makes the same claim about the marker that replaced it
// ratchet: test_removed TestQueueBypass_IsHonouredOnlyUnderTheMutantsTargetDir: the bypass is keyed on the gate's marker rather than on a path shape, so there is no per-directory answer left to pin; TestQueueBypass_IsHonouredForAGateMarkedRunAnywhere states the rule that replaced it
// ratchet: test_removed TestQueueBypass_RefusesWhenTheMutantsTargetCannotBeResolved: MutantsTargetDir is deleted with the path-shaped bypass, so an unresolvable root is no longer an input the answer depends on

// markGateRun puts the marker `aphrollo gate mutants run` gives its children
// into this process's environment. It is the whole handshake: the gate's
// runner holds the box-wide mutation lock around the call, and this is what
// says so to the shim the call goes through.
func markGateRun(t *testing.T) {
	t.Helper()
	t.Setenv(tdd.MutationGateEnv, tdd.MutationGateMarked)
}

// Two bare `cargo mutants` runs were measured holding the machine-wide build
// lock for hours while building cold tree copies in the OS temp dir: post-edit
// hooks waited up to 619 s and 56 were deferred in three hours. The shim
// refuses the invocation that does that, and names the one command that does
// it right.
func TestRunCargoShim_RefusesABareCargoMutants(t *testing.T) {
	withIsolatedCargoLock(t)

	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"mutants", "--in-diff", "lane.diff"}, strings.NewReader(""), &stdout, &stderr, testCargoShimConfig())
	if code == 0 {
		t.Fatal("a bare cargo mutants must not run")
	}
	want := "gate: run `aphrollo gate mutants run` — bare cargo mutants builds a cold copy in the OS temp dir and holds the build lock for hours, and a producer invoked directly runs outside the box-wide mutation lock"
	if got := strings.TrimSpace(stderr.String()); got != want {
		t.Fatalf("stderr = %q, want exactly %q", got, want)
	}
}

// A leading toolchain override must not let a bare `cargo mutants` sail past
// the refusal: `cargo +nightly mutants` is the exact class of invocation the
// refusal exists to stop, and a hidden verb used to let it straight through.
func TestRunCargoShim_RefusesABareCargoMutantsWithALeadingToolchainOverride(t *testing.T) {
	withIsolatedCargoLock(t)

	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"+nightly", "mutants", "--in-diff", "lane.diff"}, strings.NewReader(""), &stdout, &stderr, testCargoShimConfig())
	if code == 0 {
		t.Fatal("a bare `cargo +nightly mutants` must not run either")
	}
	want := "gate: run `aphrollo gate mutants run` — bare cargo mutants builds a cold copy in the OS temp dir and holds the build lock for hours, and a producer invoked directly runs outside the box-wide mutation lock"
	if got := strings.TrimSpace(stderr.String()); got != want {
		t.Fatalf("stderr = %q, want exactly %q", got, want)
	}
}

// Through the gate's own runner the same invocation is exactly what should
// happen, and the marker in the environment is what tells them apart. The
// runner sets it and nothing else does, so the refusal above still stands for
// every hand-typed `cargo mutants`, wherever it builds.
func TestCargoShim_AllowsMutantsWhenTheGateMarkedTheRun(t *testing.T) {
	withIsolatedCargoLock(t)
	markGateRun(t)
	// An ordinary target dir: the run is recognised by the marker, never by
	// where it happens to build.
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "target"))

	cfg := testCargoShimConfig()
	// A stub that ignores its argv: for the shim to SEE the verb it has to be
	// a bare `mutants`, which is not a command any real shell would accept.
	cfg.realCargo = runVerbStub(t)
	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"mutants", "--in-diff", "d.diff"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want the gated run to proceed\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "aphrollo gate mutants run") {
		t.Fatalf("a gated run must not be refused: %q", stderr.String())
	}
}

// The bypass keeps a mutation run from serializing against every editor on
// the box. What earns it is the box-wide mutation lock the gate's runner
// holds around the whole call — a property of the CALLER, not of the
// directory it builds into — so the marker carries it and the path does not
// enter into it.
func TestQueueBypass_IsHonouredForAGateMarkedRunAnywhere(t *testing.T) {
	markGateRun(t)
	if !queueBypassAllowed() {
		t.Fatal("a run the gate marked holds the mutation lock already and must not queue behind the editors on the box")
	}
	t.Setenv(tdd.MutationGateEnv, "")
	if queueBypassAllowed() {
		t.Fatal("without the marker, nothing bypasses the build queue")
	}
}

// And the shim acts on that: with the lock held by somebody else, a marked
// run goes ahead immediately and says nothing about a queue.
func TestRunCargoShim_BypassRunsWhileAnotherBuildHoldsTheLock(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "target"))
	markGateRun(t)

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir(), "cargo nextest run -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the isolated lock")
	}
	defer release()

	cfg := testCargoShimConfig()
	cfg.realCargo = runVerbStub(t)
	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"mutants", "--in-diff", "d.diff"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want the bypassing run to proceed\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "queued behind") {
		t.Fatalf("a bypassing run must not queue: %q", stderr.String())
	}
	// It also leaves the real holder's record alone: it never took the slot.
	owner, ok := tdd.ReadBuildSlotOwner(shimTargetDir())
	if !ok || !strings.Contains(owner.Cmd, "other-crate") {
		t.Fatalf("the bypass claimed a slot it does not hold: %+v (present=%v)", owner, ok)
	}
}

// The bypass is a tolerated hole: any process can set the marker and skip the
// build queue. The harm is bounded to that run's own target dir, and what
// makes it tolerable is that every use is COUNTED — a bypass nobody can see
// is a bypass nobody manages.
func TestQueueBypass_IsLoggedOncePerProcess(t *testing.T) {
	cfg := gateConfigDir(t)
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "target"))
	markGateRun(t)
	resetBypassLog()

	cfgShim := testCargoShimConfig()
	cfgShim.realCargo = runVerbStub(t)
	var stdout, stderr bytes.Buffer
	for i := 0; i < 3; i++ {
		if code := runCargoShim([]string{"build"}, strings.NewReader(""), &stdout, &stderr, cfgShim); code != 0 {
			t.Fatalf("exit = %d, want the bypassing run to proceed\nstderr: %s", code, stderr.String())
		}
	}

	if n := countGateLogVerdict(t, cfg, "queue-bypass"); n != 1 {
		t.Fatalf("queue-bypass logged %d times over three invocations, want exactly once per process", n)
	}
}

// countGateLogVerdict counts the lines whose verdict field is want.
func countGateLogVerdict(t *testing.T, cfg, want string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, " "+want+" ") {
			n++
		}
	}
	return n
}
