package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// mutantsTargetDirForTest is a CARGO_TARGET_DIR shaped like the dedicated
// mutants worktree's own — the shape `refuseBareMutants` recognises a gated
// run by, in place of any environment handshake.
func mutantsTargetDirForTest(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".worktrees", "repo", "mutants", "target")
}

// Two bare `cargo mutants` runs were measured holding the machine-wide build
// lock for hours while building cold tree copies in the OS temp dir: post-edit
// hooks waited up to 619 s and 56 were deferred in three hours. The shim
// refuses the invocation that does that, and names the one command that does
// it right. Building somewhere other than the mutants worktree's own target
// dir is what makes it "bare" — no handshake variable rescues it, including
// the retired MUTATION_GATE, which the shim no longer reads at all.
func TestRunCargoShim_RefusesABareCargoMutants(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv(tdd.MutationGateEnv, "1")

	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"mutants", "--in-diff", "lane.diff"}, strings.NewReader(""), &stdout, &stderr, testCargoShimConfig())
	if code == 0 {
		t.Fatal("a bare cargo mutants must not run, MUTATION_GATE=1 or not — that variable is dead")
	}
	want := "gate: run tools/mutation_gate.sh <base> — bare cargo mutants builds a cold copy in the OS temp dir and holds the build lock for hours"
	if got := strings.TrimSpace(stderr.String()); got != want {
		t.Fatalf("stderr = %q, want exactly %q", got, want)
	}
}

// A leading toolchain override must not let a bare `cargo mutants` sail past
// the refusal: `cargo +nightly mutants` is the exact class of invocation the
// refusal exists to stop, and a hidden verb used to let it straight through.
func TestRunCargoShim_RefusesABareCargoMutantsWithALeadingToolchainOverride(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv(tdd.MutationGateEnv, "1")

	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"+nightly", "mutants", "--in-diff", "lane.diff"}, strings.NewReader(""), &stdout, &stderr, testCargoShimConfig())
	if code == 0 {
		t.Fatal("a bare `cargo +nightly mutants` must not run either")
	}
	want := "gate: run tools/mutation_gate.sh <base> — bare cargo mutants builds a cold copy in the OS temp dir and holds the build lock for hours"
	if got := strings.TrimSpace(stderr.String()); got != want {
		t.Fatalf("stderr = %q, want exactly %q", got, want)
	}
}

// APHROLLO_MUTATION_GATE is the one-release grace for a caller still using
// the old handshake: let through even outside the mutants worktree, but
// counted, so the removal shows up before it breaks anyone.
func TestRunCargoShim_AllowsCargoMutantsThroughTheDeprecatedAliasAndLogsIt(t *testing.T) {
	withIsolatedCargoLock(t)
	cfg := gateConfigDir(t)
	t.Setenv(deprecatedMutationGateEnv, "1")
	resetDeprecatedMutationGateLog()
	t.Cleanup(resetDeprecatedMutationGateLog)

	cfgShim := testCargoShimConfig()
	cfgShim.realCargo = runVerbStub(t)
	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"mutants", "--in-diff", "d.diff"}, strings.NewReader(""), &stdout, &stderr, cfgShim)
	if code != 0 {
		t.Fatalf("exit = %d, want the deprecated alias to still be honoured\nstderr: %s", code, stderr.String())
	}
	if n := countGateLogVerdict(t, cfg, "mutation-gate-env-deprecated"); n != 1 {
		t.Fatalf("mutation-gate-env-deprecated logged %d times, want exactly once", n)
	}
}

// Through the gate's own runner the same invocation is exactly what should
// happen, and building into the dedicated mutants worktree's own target dir
// is what tells them apart.
func TestRunCargoShim_AllowsCargoMutantsThroughTheGatesRunner(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv("CARGO_TARGET_DIR", mutantsTargetDirForTest(t))

	cfg := testCargoShimConfig()
	// A stub that ignores its argv: for the shim to SEE the verb it has to be
	// a bare `mutants`, which is not a command any real shell would accept.
	cfg.realCargo = runVerbStub(t)
	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"mutants", "--in-diff", "d.diff"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want the gated run to proceed\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "mutation_gate.sh") {
		t.Fatalf("a gated run must not be refused: %q", stderr.String())
	}
}

// The bypass is what keeps a mutation run from serializing against every
// editor on the box. It is honoured ONLY for the dedicated mutants worktree's
// own target dir: anywhere else it would let an ordinary build walk past every
// waiter into a directory another build owns.
func TestQueueBypass_IsHonouredOnlyUnderTheMutantsTargetDir(t *testing.T) {
	parent := t.TempDir()
	mutants := filepath.Join(parent, ".worktrees", "borld", "mutants", "target-mutants")
	ordinary := filepath.Join(parent, "borld", "target")

	t.Setenv(tdd.QueueEnv, tdd.QueueBypass)
	if !queueBypassAllowed(mutants) {
		t.Fatalf("the mutants worktree's own target dir (%s) must bypass the queue", mutants)
	}
	if !queueBypassAllowed(filepath.Join(mutants, "debug", "deps")) {
		t.Fatal("a directory under the mutants target dir is still the mutation run's own")
	}
	if queueBypassAllowed(ordinary) {
		t.Fatalf("an ordinary target dir (%s) must never bypass the queue", ordinary)
	}
	t.Setenv(tdd.QueueEnv, "")
	if queueBypassAllowed(mutants) {
		t.Fatal("without the environment asking for it, nothing bypasses")
	}
}

// And the shim acts on that: with the lock held by somebody else, a bypassing
// run goes ahead immediately and says nothing about a queue.
func TestRunCargoShim_BypassRunsWhileAnotherBuildHoldsTheLock(t *testing.T) {
	withIsolatedCargoLock(t)
	target := filepath.Join(t.TempDir(), ".worktrees", "borld", "mutants", "target-mutants")
	t.Setenv("CARGO_TARGET_DIR", target)
	t.Setenv(tdd.QueueEnv, tdd.QueueBypass)
	t.Setenv(tdd.MutationGateEnv, "1")

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

// The bypass is a tolerated hole: any process can set APHROLLO_QUEUE=bypass
// with a target dir shaped like the mutation run's and skip the build queue.
// The harm is bounded to that one target dir, and what makes it tolerable is
// that every use is COUNTED — a bypass nobody can see is a bypass nobody
// manages.
func TestQueueBypass_IsLoggedOncePerProcess(t *testing.T) {
	cfg := gateConfigDir(t)
	target := filepath.Join(t.TempDir(), ".worktrees", "borld", "mutants", "target-mutants")
	t.Setenv("CARGO_TARGET_DIR", target)
	t.Setenv(tdd.QueueEnv, tdd.QueueBypass)
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
