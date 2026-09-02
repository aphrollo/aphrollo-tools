package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestCargoChildEnv_TokenReachesALongVerbsChildren pins the environment
// contract behind the mutation-run outage: the long phase's children must be
// told which slot their parent holds, and the launched process of a
// `cargo run` must NOT be — it outlives the slot entirely, so a token there
// would let an unaccounted build skip the semaphore for as long as the game
// is open.
func TestCargoChildEnv_TokenReachesALongVerbsChildren(t *testing.T) {
	slot := tdd.BuildSlot{Lock: `C:\temp\aphrollo-cargo-build.abc.lock`, Jobs: 7}

	withToken := cargoChildEnvToken(false, tdd.SlotTokenEnvEntry(slot))
	if !containsEnvKV(withToken, tdd.SlotTokenEnvName()+"="+slot.Lock) {
		t.Fatalf("env = %v, want the parent's slot token", withToken)
	}
	if containsEnvKV(withToken, tdd.BuildLockHeldEnv+"=1") {
		t.Fatal("a token child must still queue for its own TARGET lock, so it may not claim the lock is held")
	}

	t.Setenv(tdd.SlotTokenEnvName(), slot.Lock)
	launched := cargoChildEnv(false)
	for _, kv := range launched {
		if strings.HasPrefix(kv, tdd.SlotTokenEnvName()+"=") {
			t.Fatal("a launched cargo-run process must not inherit the token: it outlives the slot")
		}
	}
}

// TestLongVerb_HoldsOneSlotAndLendsIt pins the shim's half end to end with a
// stub cargo that records what it was handed: the long phase runs with the
// parent's slot token, the caller's TARGET lock is free by then (the mutation
// copies build elsewhere), and the global slot is still held — so the run
// costs one slot, not every slot on the box.
func TestLongVerb_HoldsOneSlotAndLendsIt(t *testing.T) {
	t.Setenv("APHROLLO_BUILD_SLOTS", "1")
	// Restored on the way out: the override outliving the test points every
	// later case at a t.TempDir() that testing has already removed, and an
	// unopenable lock file reads as HELD.
	t.Cleanup(tdd.SetLockDirForTest(t.TempDir()))
	dir := chdirCargoProject(t)
	stub := runVerbStub(t)
	envOut := filepath.Join(t.TempDir(), "child-env")
	t.Setenv("APHROLLO_TEST_STUB_ENV_OUT", envOut)

	code := runCargoShim([]string{"mutants", "--in-diff", "d"}, strings.NewReader(""), io.Discard, io.Discard,
		cargoShimConfig{realCargo: stub})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}

	token, err := os.ReadFile(envOut + ".mutants")
	if err != nil {
		t.Fatalf("the long phase left no record of its environment: %v", err)
	}
	if len(token) == 0 {
		t.Fatal("the long phase ran with no slot token — its children would each take a slot of their own")
	}
	if prewarm, err := os.ReadFile(envOut + ".build"); err == nil && len(prewarm) != 0 {
		t.Fatalf("the PREWARM carried a token (%s): it holds the slot itself", prewarm)
	}

	// Everything is released once the verb exits.
	if _, release, ok := tdd.TryAcquireBuildSlot(tdd.ResolveCargoTargetDir(dir), "cargo nextest run -p other-crate", "/some/other/repo"); !ok {
		t.Fatal("the long verb did not release its locks at exit")
	} else {
		release()
	}
}
