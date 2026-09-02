package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Two bare `cargo mutants` runs were measured holding the machine-wide build
// lock for hours while building cold tree copies in the OS temp dir: post-edit
// hooks waited up to 619 s and 56 were deferred in three hours. The shim
// refuses the invocation that does that, and names the one command that does
// it right.
func TestRunCargoShim_RefusesABareCargoMutants(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv(tdd.MutationGateEnv, "")

	var stdout, stderr bytes.Buffer
	code := runCargoShim([]string{"mutants", "--in-diff", "lane.diff"}, strings.NewReader(""), &stdout, &stderr, testCargoShimConfig())
	if code == 0 {
		t.Fatal("a bare cargo mutants must not run")
	}
	want := "gate: run tools/mutation_gate.sh <base> — bare cargo mutants builds a cold copy in the OS temp dir and holds the build lock for hours"
	if got := strings.TrimSpace(stderr.String()); got != want {
		t.Fatalf("stderr = %q, want exactly %q", got, want)
	}
}

// Through the gate's own runner the same invocation is exactly what should
// happen, and the marker the runner sets is what tells them apart.
func TestRunCargoShim_AllowsCargoMutantsThroughTheGatesRunner(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv(tdd.MutationGateEnv, "1")

	var stdout, stderr bytes.Buffer
	code := runCargoShim(append([]string{"mutants"}, stubCargoArgsExit(0)...), strings.NewReader(""), &stdout, &stderr, testCargoShimConfig())
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

	var stdout, stderr bytes.Buffer
	code := runCargoShim(append([]string{"mutants"}, stubCargoArgsExit(0)...), strings.NewReader(""), &stdout, &stderr, testCargoShimConfig())
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
