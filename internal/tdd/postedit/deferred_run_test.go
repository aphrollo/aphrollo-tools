package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// echoHeldEnv is a command that prints the build-lock-held marker's value, so
// a test can see what the phase's CHILD actually inherited.
func writeMarkerCmd(path string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo ran > " + path}
	}
	return []string{"sh", "-c", "echo ran > " + path}
}

func echoHeldEnv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo held=%" + BuildLockHeldEnv + "%"}
	}
	return []string{"sh", "-c", "echo held=$" + BuildLockHeldEnv}
}

// TestRunPhase_ChildSeesTheLockAsHeld pins the deadlock fix measured on
// D:/Projects/borld: the wrapper holds the build slot, then spawns cargo — and
// a session with the cargo-queue shim on PATH resolves "cargo" to the shim,
// which queued behind THE WRAPPER'S OWN slot record ("queued behind ... (pid
// 48848)") and sat there until it gave up. The child must be told the lock is
// already held by this process, exactly as runCargoLocked tells its own child.
func TestRunPhase_ChildSeesTheLockAsHeld(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	job := DeferredJob{
		Project: dir, Phase: "run", Dir: dir, Runner: echoHeldEnv(),
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json"),
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	jobPath := filepath.Join(dir, "job.json")
	if err := os.WriteFile(jobPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if code := RunPhase(jobPath); code != 0 {
		t.Fatalf("RunPhase exit = %d, want 0", code)
	}

	logged, err := os.ReadFile(job.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "held=1") {
		t.Fatalf("child saw %q, want held=1 — a nested cargo shim would queue behind this very phase", strings.TrimSpace(string(logged)))
	}
}

// writeJob saves a job record and returns the path the wrapper reads.
func writeJob(t *testing.T, j DeferredJob) string {
	t.Helper()
	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.Dir, "job.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunPhase_AlwaysWritesAResult pins the liveness contract from the OTHER
// side: the result file is the only thing that says a phase is over, so a
// wrapper that returns early (empty runner, unusable log path) leaves the
// next hook reporting BUILDING until the 600s ceiling for work that never
// ran.
func TestRunPhase_AlwaysWritesAResult(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: nil,
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}
	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done {
		t.Fatal("no result for a job the wrapper refused to run")
	}
	if out.ExitCode == 0 {
		t.Fatalf("result = %+v, want a failing exit for a phase that never ran", out)
	}
}

// TestRunPhase_FailsWhenItCannotHoldASlot pins the invariant the whole
// governor rests on: running the build anyway would compile into a target
// dir another build owns, and without the HELD marker the shimmed cargo
// inside would queue for the full wait on the slot this phase could not get.
func TestRunPhase_FailsWhenItCannotHoldASlot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(deferredMaxEnv, "1")
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")

	_, release, ok := TryAcquireBuildSlot(ResolveCargoTargetDir(dir), "cargo build", "/repo")
	if !ok {
		t.Fatal("could not occupy the slot")
	}
	defer release()

	// argv[0] is "cargo": only a cargo runner takes the build-slot governor
	// at all (issue #354), and the denied branch returns before ever calling
	// exec.Command — "cargo" is never actually spawned here, so this needs
	// no real cargo on PATH.
	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: append([]string{"cargo"}, writeMarkerCmd(marker)[1:]...),
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}
	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done || out.ExitCode == 0 {
		t.Fatalf("result = %+v (done %v), want a failure — no slot means no build", out, done)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the phase ran without a slot")
	}
}

// ratchet: test_removed TestRunPhase_NonCargoRunnerNeverTakesABuildSlot: asserted
// the first version of issue #354's fix, which skipped ALL slot governance
// (not just cargo's target-dir flock) for a non-cargo runner — a cold review
// caught that this also dropped it out of the box-wide OOM/CPU cap. Split
// into TestRunPhase_NonCargoRunnerNeverContendsOnACargoTargetLock (the
// target-dir flock is still skipped) and
// TestRunPhase_NonCargoRunnerStillRespectsTheGlobalSlotCap (the global cap
// still applies) below.

// TestRunPhase_NonCargoRunnerNeverContendsOnACargoTargetLock pins issue
// #354's fix: a `go test`/pytest/npm runner has no target dir to contend
// for, so it must not queue behind cargo's target-dir flock even when that
// lock is held elsewhere — it still takes a global slot (see the sibling
// test below for that half).
func TestRunPhase_NonCargoRunnerNeverContendsOnACargoTargetLock(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	shared := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", shared)

	// A cargo build occupies the SHARED target dir's lock (and one global
	// slot with it) — the exact shape (a shell-exported CARGO_TARGET_DIR)
	// that used to key every non-cargo run in the same environment onto
	// this same lock, serialising an unrelated Go project against an
	// unrelated cargo build for no reason (issue #354).
	_, release, ok := TryAcquireBuildSlot(ResolveCargoTargetDir(shared), "cargo build", "/repo")
	if !ok {
		t.Fatal("could not occupy the shared target lock")
	}
	defer release()

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: writeMarkerCmd(marker),
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}
	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done {
		t.Fatal("no result written")
	}
	if out.ExitCode != 0 {
		t.Fatalf("result = %+v, want a real run — a non-cargo phase must never wait on a cargo target-dir lock it never asked for", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the phase never ran despite the box having a free global slot: %v", err)
	}
}

// TestRunPhase_NonCargoRunnerStillRespectsTheGlobalSlotCap pins the other
// half of the same fix: dropping the cargo-specific TARGET lock for a
// non-cargo runner must not drop the box-wide OOM/CPU governor too — the
// global slots ARE that governor (buildslots.go's header comment), not a
// cargo-only mechanism. A non-cargo runner that skipped them entirely (this
// fix's first version) ran fully unbounded; a cold review caught it.
func TestRunPhase_NonCargoRunnerStillRespectsTheGlobalSlotCap(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(deferredMaxEnv, "1")
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")

	// Occupy every global slot, exactly as a box saturated with cargo builds
	// would.
	var releases []func()
	for range buildSlotCount() {
		_, release, ok := TryAcquireBuildSlot(ResolveCargoTargetDir(t.TempDir()), "cargo build", "/repo")
		if !ok {
			t.Fatal("could not occupy a slot")
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()

	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: writeMarkerCmd(marker),
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}
	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done || out.ExitCode == 0 {
		t.Fatalf("result = %+v (done %v), want a denial — the box-wide slot cap must still apply to a non-cargo runner", out, done)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the phase ran despite every global slot being held elsewhere")
	}
}

// TestRunPhase_StampsStartedWhenItGetsTheSlot pins the clock the abandon
// rule reads: a phase that queued for the slot was already burning its
// 600s ceiling before it had begun, so a busy box could kill a build the
// moment it finally started.
func TestRunPhase_StampsStartedWhenItGetsTheSlot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: echoHeldEnv(),
		Started: time.Now().Add(-30 * time.Minute)}
	saveDeferredJob(j)
	saved, _ := loadDeferredJob("", dir)
	RunPhase(writeJob(t, saved))

	after, ok := loadDeferredJob("", dir)
	if !ok {
		t.Fatal("the job record vanished")
	}
	if time.Since(after.Started) > time.Minute {
		t.Fatalf("Started = %s, want it stamped when the slot was taken", after.Started)
	}
}

// TestRecordSpawnedProcess_NeverRevertsAStartedTheChildAlreadyAdvanced pins
// the third writer #294 originally left outside the lock: spawnPhase's own
// post-cmd.Start() write of PID/PIDCreatedAt used to be a bare saveDeferredJob
// of a snapshot taken BEFORE Start() returned. The detached child reaches
// stampDeferredStart within microseconds — for a non-cargo runner it skips
// the build-slot wait entirely — so its properly-locked write could land
// FIRST, and the parent's unlocked write would then stomp Started back down
// to the earlier spawn time: exactly the "killable the moment it finally
// started" bug stampDeferredStart's own redesign exists to avoid.
func TestRecordSpawnedProcess_NeverRevertsAStartedTheChildAlreadyAdvanced(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root})

	// The child's stampDeferredStart fires first, advancing Started past the
	// spawn time the parent captured before it.
	spawnTime := time.Now()
	buildStarted := spawnTime.Add(50 * time.Millisecond)
	stampDeferredStart("", root, buildStarted)

	recordSpawnedProcess("", root, 4242, time.Time{}, spawnTime)

	got, ok := loadDeferredJob("", root)
	if !ok {
		t.Fatal("the job must survive")
	}
	if got.PID != 4242 {
		t.Fatalf("PID = %d, want the spawned pid recorded", got.PID)
	}
	if !got.Started.Equal(buildStarted) {
		t.Fatalf("Started = %s, want the child's build-start time %s preserved, not reverted to spawn time", got.Started, buildStarted)
	}
}

// TestRecordSpawnedProcess_SetsStartedWhenTheChildHasNotYet pins the other
// half: before the child ever calls stampDeferredStart, Started must not be
// left at its zero value — deferredExpired would read that as "running since
// the epoch" and abandon a phase that only just began.
func TestRecordSpawnedProcess_SetsStartedWhenTheChildHasNotYet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root})

	spawnTime := time.Now()
	recordSpawnedProcess("", root, 4242, time.Time{}, spawnTime)

	got, ok := loadDeferredJob("", root)
	if !ok {
		t.Fatal("the job must survive")
	}
	if !got.Started.Equal(spawnTime) {
		t.Fatalf("Started = %s, want the spawn time %s recorded", got.Started, spawnTime)
	}
}

// TestDeferredSlotWait_IsAFractionOfTheMaximum pins the same defect at the
// budget level: queuing for the full ceiling leaves a phase eligible for the
// abandon kill the instant it starts to build.
func TestDeferredSlotWait_IsAFractionOfTheMaximum(t *testing.T) {
	t.Setenv(deferredMaxEnv, "600")
	if wait, max := deferredSlotWait(), deferredMax(); wait >= max {
		t.Fatalf("slot wait %s vs max %s — a phase must not be able to spend its whole life queuing", wait, max)
	}
}

// TestRunPhase_ReplacedWhileQueuedSaysSoAndNeverBuilds pins the coalescing
// half of issue #830 at the phase wrapper: a deferred build still queued for
// its slot when a newer identical request arrives gives up without building,
// and its log names the replacement rather than a full box, so the harvest's
// infra-failed line says what actually happened.
func TestRunPhase_ReplacedWhileQueuedSaysSoAndNeverBuilds(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(deferredMaxEnv, "60")
	withIsolatedBuildLock(t)
	queued := make(chan string, 4)
	defer SetSlotRequestQueuedHookForTest(func(path string) { queued <- path })()
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	target := ResolveCargoTargetDir(dir)
	_, release, ok := TryAcquireBuildSlot(target, "cargo build", "/repo")
	if !ok {
		t.Fatal("could not occupy the slot")
	}
	defer release()

	j := DeferredJob{Project: dir, Phase: "build", Dir: dir, Runner: append([]string{"cargo"}, writeMarkerCmd(marker)[1:]...),
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}
	older := make(chan struct{})
	go func() { RunPhase(writeJob(t, j)); close(older) }()
	select {
	case <-queued:
	case <-time.After(2 * time.Second):
		t.Fatal("the request never queued")
	}
	newer := make(chan SlotWait, 1)
	go func() {
		_, rel, wait := acquireQueuedBuildSlot(target, 100*time.Millisecond, cmdString(runnerFromArgv(j.Runner, j.Dir)), j.Dir)
		rel()
		newer <- wait
	}()
	select {
	case <-older:
	case <-time.After(10 * time.Second):
		t.Fatal("the replaced phase never returned")
	}
	select {
	case <-newer:
	case <-time.After(10 * time.Second):
		t.Fatal("the newer request never ended")
	}

	out, done := deferredResult(j)
	if !done || !out.SetupFailed {
		t.Fatalf("result = %+v (done %v), want a setup failure: the replaced phase never built", out, done)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the replaced phase built anyway")
	}
	if logged := deferredLog(j); !strings.Contains(logged, "superseded") {
		t.Fatalf("log = %q, want it to name the newer request that replaced this one", logged)
	}
}
