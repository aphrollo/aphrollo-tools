package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// Serial: installs a process-wide lock-wait threshold and appends to gate.log
// under its own CLAUDE_CONFIG_DIR.
// TestLogLockWait_OnlyAWaitPastTheThresholdIsLogged pins the guard: a wait under
// the threshold leaves gate.log alone, one at or past it logs a lock-wait line.
func TestLogLockWait_OnlyAWaitPastTheThresholdIsLogged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Cleanup(SetLockWaitLogThresholdForTest(time.Second))
	root := t.TempDir()
	r := Runner{Cmd: "cargo", Args: []string{"test"}}

	logLockWait("precommit", root, r, 500*time.Millisecond)
	if got := tddtest.GateLogContent(t, filepath.Join(cfg, "gate-state", "gate.log")); got != "" {
		t.Fatalf("a short wait was logged:\n%s", got)
	}
	logLockWait("precommit", root, r, 2*time.Second)
	requireLoggedVerdict(t, cfg, "lock-wait")
}

// Serial: points the process-wide build lock at its own file.
// TestRunCargoLocked_AFloorRestoresTheBudgetTheQueueShrank pins the #660 floor:
// after a real wait the run gets back the time its own record justifies, up to
// the stage budget, instead of only what the wait left over.
func TestRunCargoLocked_AFloorRestoresTheBudgetTheQueueShrank(t *testing.T) {
	withIsolatedBuildLock(t)
	const holdFor = 150 * time.Millisecond
	root := t.TempDir()
	_, release, ok := acquireBuildSlot(resolveTargetDir(os.Getenv, root), time.Second, "cargo test -p other", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the build slot")
	}
	go func() {
		time.Sleep(holdFor)
		release()
	}()

	var gotDeadline time.Time
	stub := func(r Runner, _ string) SuiteResult {
		gotDeadline = r.Deadline
		return SuiteResult{Passed: true}
	}
	const stageBudget = time.Second
	_, waited, acquired := runCargoLocked(stub, Runner{Cmd: "cargo"}, root, 2*time.Second, stageBudget, stageBudget)
	if !acquired || waited < holdFor/2 {
		t.Fatalf("setup: acquired=%v waited=%s, want a real wait", acquired, waited)
	}
	// Without the floor the remaining budget is stageBudget - waited (~850ms);
	// with a floor of the whole budget it is restored to ~stageBudget.
	if remaining := time.Until(gotDeadline); remaining < stageBudget-80*time.Millisecond {
		t.Fatalf("remaining budget = %s, want it restored to ~%s by the floor (waited %s)", remaining, stageBudget, waited)
	}
}

// Serial: points the process-wide build lock at its own file, and reads
// CARGO_TARGET_DIR from the process-wide environment.
// TestTargetDirSharing_ALiveHolderInAnotherCheckoutMakesTheTargetShared pins
// the second sharing condition: a target dir inside this checkout is still
// exposed when another checkout holds a build slot on it right now.
func TestTargetDirSharing_ALiveHolderInAnotherCheckoutMakesTheTargetShared(t *testing.T) {
	withIsolatedBuildLock(t)
	ws := staleCrate(t)
	target := ws + "/target"
	t.Setenv("CARGO_TARGET_DIR", target)
	other := t.TempDir()

	var share targetDirShare
	var shared bool
	stub := func(Runner, string) SuiteResult {
		share, shared = targetDirSharing(ws)
		return SuiteResult{Passed: true}
	}
	if _, _, acquired := runCargoLocked(stub, Runner{Cmd: "cargo", Args: []string{"test"}, Dir: other}, ws, time.Second, time.Second, 0); !acquired {
		t.Fatal("setup: the slot must be acquired")
	}
	if !shared || share.dir == "" || share.reason == "" {
		t.Fatalf("targetDirSharing = (%+v, %v), want a shared target held from another checkout", share, shared)
	}
	if want := "another checkout is building into its target dir"; !strings.Contains(share.reason, want) {
		t.Fatalf("reason = %q, want it to say %q", share.reason, want)
	}
}
