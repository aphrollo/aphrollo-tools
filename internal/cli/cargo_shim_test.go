package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// withIsolatedCargoLock points the build lock (and its owner file) at a
// per-test path for the duration of the test, so these tests never race the
// box's own aphrollo PostToolUse hook exercising the REAL machine-wide lock
// file -- same reasoning as internal/tdd's withIsolatedBuildLock.
func withIsolatedCargoLock(t *testing.T) {
	t.Helper()
	restore := tdd.SetBuildLockPathForTest(filepath.Join(t.TempDir(), "test-build.lock"))
	t.Cleanup(restore)
	// One slot per target dir: these tests are about the shim's waiting
	// BEHAVIOUR, so the target dir must be saturated by a single holder.
	t.Setenv("APHROLLO_BUILD_SLOTS", "1")
}

// stubCargo returns a trivial, always-available "real cargo" stand-in: on
// Windows, cmd.exe; elsewhere, sh.
func stubCargo() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}

func stubCargoArgsExit(code int) []string {
	if runtime.GOOS == "windows" {
		return []string{"/C", "exit", strconv.Itoa(code)}
	}
	return []string{"-c", "exit " + strconv.Itoa(code)}
}

func testCargoShimConfig() cargoShimConfig {
	return cargoShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    stubCargo(),
	}
}

// TestRunCargoShim_UncontendedIsSilent pins the coordinator's revised
// directive: when the lock is free, the shim must print NOTHING to stderr --
// no queued line, no acquired line -- and simply run the command.
func TestRunCargoShim_UncontendedIsSilent(t *testing.T) {
	withIsolatedCargoLock(t)
	cfg := testCargoShimConfig()

	var stdout, stderr bytes.Buffer
	code := runCargoShim(stubCargoArgsExit(0), strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("uncontended run must print nothing to stderr, got: %q", stderr.String())
	}
}

// TestRunCargoShim_ExitCodePropagation pins that the REAL cargo's exit code
// passes straight through, uncontended.
func TestRunCargoShim_ExitCodePropagation(t *testing.T) {
	withIsolatedCargoLock(t)
	cfg := testCargoShimConfig()

	var stdout, stderr bytes.Buffer
	code := runCargoShim(stubCargoArgsExit(7), strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (propagated from the stub)", code)
	}
}

// TestRunCargoShim_OwnerFileRemovedAfterRun pins cleanup: once the shim
// finishes (success or failure), the owner file it wrote while holding the
// lock must be gone.
func TestRunCargoShim_OwnerFileRemovedAfterRun(t *testing.T) {
	withIsolatedCargoLock(t)
	cfg := testCargoShimConfig()

	var stdout, stderr bytes.Buffer
	runCargoShim(stubCargoArgsExit(0), strings.NewReader(""), &stdout, &stderr, cfg)

	if _, ok := tdd.ReadBuildSlotOwner(shimTargetDir()); ok {
		t.Fatal("owner file must be removed once the shim's run completes")
	}
}

// TestRunCargoShim_PassthroughWhenBuildLockHeldEnvSet pins requirement (d):
// with APHROLLO_BUILD_LOCK_HELD=1 set (a hooks/gate cargo run already holds
// the lock), the shim must run WITHOUT ever touching the lock at all -- not
// even a non-blocking try -- proven here by having the LOCK ALREADY HELD by
// the test itself (simulating the real scenario) and confirming the shim
// still succeeds immediately, silently, without waiting or writing its own
// owner file (which would incorrectly claim ownership out from under the
// real holder).
func TestRunCargoShim_PassthroughWhenBuildLockHeldEnvSet(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv(tdd.BuildLockHeldEnv, "1")

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
	if !ok {
		t.Fatal("setup: must be able to take the isolated lock")
	}
	defer release()

	cfg := testCargoShimConfig()
	var stdout, stderr bytes.Buffer
	code := runCargoShim(stubCargoArgsExit(0), strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("passthrough run exit = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("passthrough must print nothing (it never touches the lock), got: %q", stderr.String())
	}
	if _, ok := tdd.ReadBuildSlotOwner(shimTargetDir()); ok {
		t.Fatal("passthrough must never write its own owner file -- the real holder's is the only valid one")
	}
}

// TestRunCargoShim_WaitsPrintsQueuedOnceAndAcquiredOnce pins the revised
// directive precisely: exactly ONE "queued behind" line (naming the holder
// from the owner file) when contention is first discovered, exactly ONE
// "lock acquired after" line once it succeeds -- never a repeated/periodic
// line -- and the run completes successfully once the holder releases.
func TestRunCargoShim_WaitsPrintsQueuedOnceAndAcquiredOnce(t *testing.T) {
	withIsolatedCargoLock(t)

	slot, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
	if !ok {
		t.Fatal("setup: must be able to take the isolated lock")
	}
	tdd.WriteBuildSlotOwner(slot, "cargo nextest run -p other-crate", "/some/other/repo")
	go func() {
		time.Sleep(80 * time.Millisecond)
		tdd.RemoveBuildSlotOwner(slot)
		release()
	}()

	cfg := cargoShimConfig{
		waitBudget:   2 * time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    stubCargo(),
	}
	var stdout, stderr bytes.Buffer
	code := runCargoShim(stubCargoArgsExit(0), strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (must eventually acquire and succeed)", code)
	}

	out := stderr.String()
	if n := strings.Count(out, "queued behind"); n != 1 {
		t.Fatalf("expected exactly ONE queued line, got %d in: %s", n, out)
	}
	if n := strings.Count(out, "lock acquired after"); n != 1 {
		t.Fatalf("expected exactly ONE acquired line, got %d in: %s", n, out)
	}
	wantHolderCmd := "\"cargo nextest run -p other-crate\""
	if !strings.Contains(out, wantHolderCmd) || !strings.Contains(out, "/some/other/repo") {
		t.Fatalf("expected the queued line to name the holder's cmd/cwd, got: %s", out)
	}
}

// TestRunCargoShim_GivesUpAfterWaitBudget_Exits75 pins requirement (b): a
// lock held for longer than waitBudget makes the shim give up, exit 75
// (EX_TEMPFAIL), and print the give-up line -- all within a short, bounded
// time (no real sleeps > 200ms in this suite, so both the budget and the
// bound below stay well under that).
func TestRunCargoShim_GivesUpAfterWaitBudget_Exits75(t *testing.T) {
	withIsolatedCargoLock(t)

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
	if !ok {
		t.Fatal("setup: must be able to take the isolated lock")
	}
	t.Cleanup(release)

	cfg := cargoShimConfig{
		waitBudget:   100 * time.Millisecond,
		pollInterval: 15 * time.Millisecond,
		realCargo:    stubCargo(),
	}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := runCargoShim(stubCargoArgsExit(0), strings.NewReader(""), &stdout, &stderr, cfg)
	elapsed := time.Since(start)

	if code != exCargoTempFail {
		t.Fatalf("exit = %d, want %d (EX_TEMPFAIL)", code, exCargoTempFail)
	}
	if elapsed > cfg.waitBudget+2*time.Second {
		t.Fatalf("give-up took %s, want close to the %s wait budget", elapsed, cfg.waitBudget)
	}
	if !strings.Contains(stderr.String(), "gave up after") {
		t.Fatalf("expected a give-up line, got: %s", stderr.String())
	}
}

// TestRunCargoShim_WaitBudgetZero_FailsImmediately pins requirement (a)'s
// "0 = fail immediately": a contended lock with waitBudget=0 must give up
// on the very first check, without ever sleeping.
func TestRunCargoShim_WaitBudgetZero_FailsImmediately(t *testing.T) {
	withIsolatedCargoLock(t)

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir())
	if !ok {
		t.Fatal("setup: must be able to take the isolated lock")
	}
	t.Cleanup(release)

	cfg := cargoShimConfig{
		waitBudget:   0,
		pollInterval: 15 * time.Millisecond,
		realCargo:    stubCargo(),
	}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := runCargoShim(stubCargoArgsExit(0), strings.NewReader(""), &stdout, &stderr, cfg)
	elapsed := time.Since(start)

	if code != exCargoTempFail {
		t.Fatalf("exit = %d, want %d", code, exCargoTempFail)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("waitBudget=0 must fail immediately, took %s", elapsed)
	}
}

// TestResolveRealCargo_HonorsOverride pins the APHROLLO_REAL_CARGO override
// (added specifically so tests never need to locate a real cargo install).
func TestResolveRealCargo_HonorsOverride(t *testing.T) {
	t.Setenv("APHROLLO_REAL_CARGO", "/some/fake/cargo")
	got, err := resolveRealCargo()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/some/fake/cargo" {
		t.Fatalf("resolveRealCargo() = %q, want the override", got)
	}
}

// TestResolveRealCargo_UsesCargoHome pins CARGO_HOME resolution (the
// rustup-proxy path shape: <CARGO_HOME>/bin/cargo[.exe]) when no override is
// set.
func TestResolveRealCargo_UsesCargoHome(t *testing.T) {
	t.Setenv("APHROLLO_REAL_CARGO", "")
	home := t.TempDir()
	exeName := "cargo"
	if runtime.GOOS == "windows" {
		exeName = "cargo.exe"
	}
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bin", exeName), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO_HOME", home)

	got, err := resolveRealCargo()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "bin", exeName)
	if got != want {
		t.Fatalf("resolveRealCargo() = %q, want %q", got, want)
	}
}
