package cli

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestExecCargo_MarksTheLockHeldOnlyWhenItIs pins a lie in the child's
// environment: the shim stamped APHROLLO_BUILD_LOCK_HELD=1 on EVERY cargo it
// launched, including the read-only bypass and the post-release `cargo run`
// — so a nested cargo inside a process that holds nothing skipped the queue
// entirely, for the whole life of a game the developer left running.
func TestExecCargo_MarksTheLockHeldOnlyWhenItIs(t *testing.T) {
	held := cargoChildEnv(true)
	if !containsEnvKV(held, tdd.BuildLockHeldEnv+"=1") {
		t.Fatal("a child launched under a held slot must be told so, or it queues behind its own parent")
	}
	free := cargoChildEnv(false)
	if containsEnvKV(free, tdd.BuildLockHeldEnv+"=1") {
		t.Fatal("a child launched with NO slot must not claim the lock is held")
	}
}

func containsEnvKV(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// TestCargoLongVerbs_MatchWhatTheyActuallyCompile pins two mistakes in the
// long-verb table: `bench` compiles BENCH targets, so a `build --tests`
// prewarm warmed the wrong thing and the real compile then ran unslotted;
// and `cargo watch` recompiles on every save for as long as it is open, so
// treating it as "one prewarm then unlocked forever" hands the box to a
// process that never stops building.
func TestCargoLongVerbs_MatchWhatTheyActuallyCompile(t *testing.T) {
	if isCargoLongVerb([]string{"watch", "-x", "test"}) {
		t.Fatal("cargo watch keeps compiling — it must hold a slot like any other build")
	}
	if !isCargoLongVerb([]string{"bench"}) {
		t.Fatal("cargo bench still prewarms under a slot")
	}
	if got := strings.Join(cargoPrewarmArgs([]string{"bench"}), " "); !strings.Contains(got, "--benches") {
		t.Fatalf("bench prewarm = %q, want it to build the bench targets", got)
	}
	if got := strings.Join(cargoPrewarmArgs([]string{"mutants"}), " "); !strings.Contains(got, "check") {
		t.Fatalf("mutants prewarm = %q, want a typecheck", got)
	}
}

// TestGCFlags_RejectsAZeroAge pins the footgun: `--older-than 0d` makes
// EVERY incremental cache a candidate, which is a full cold rebuild of the
// workspace dressed up as disk hygiene.
func TestGCFlags_RejectsAZeroAge(t *testing.T) {
	if _, err := tdd.ParseGCAge("0d"); err == nil {
		t.Fatal("a zero age must be rejected — it selects every cache there is")
	}
	if _, err := tdd.ParseGCAge("1h"); err != nil {
		t.Fatalf("a real age must still parse: %v", err)
	}
}
