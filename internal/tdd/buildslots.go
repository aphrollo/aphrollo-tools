package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Two locks, because there are two different constraints:
//
//   - The TARGET LOCK (one per resolved target dir) mirrors cargo's own
//     build-directory flock: exactly one build per target dir, ever.
//     Admitting a second would hand out a slot the second builder then
//     spends its whole budget blocked on INSIDE cargo, invisibly, which is
//     strictly worse than being told to wait.
//   - The GLOBAL SLOTS (N of them, APHROLLO_BUILD_SLOTS, default 2) are the
//     OOM/CPU governor: separate target dirs do not compete for a build
//     directory, but they do compete for the box. Each holder runs with
//     CARGO_BUILD_JOBS = totalJobs/N, so N builds cost about what one
//     uncapped build used to.
//
// A build needs BOTH: target lock first, then a global slot; released in
// reverse. If the slot cannot be had, the target lock goes back immediately
// -- a full box must never leave target dirs locked by builds that never
// started.

// buildSlotsEnv overrides how many concurrent builds one target dir admits.
const buildSlotsEnv = "APHROLLO_BUILD_SLOTS"

// defaultBuildSlots is the shipped slot count: two sessions building into
// one target dir at half jobs each, which is the observed sweet spot
// between "one session at a time" and the link-wave OOM an uncapped
// free-for-all produced.
const defaultBuildSlots = 2

// BuildSlot identifies an acquired build slot: which slot of the target
// dir's key, the lock and owner files it owns, and the CARGO_BUILD_JOBS
// share a build holding it should run with.
type BuildSlot struct {
	Index int
	Lock  string
	Owner string
	Jobs  int
}

// buildSlotCount reads the configured slot count, flooring at 1 (a zero or
// negative count would admit nobody) and falling back to the default for
// anything unparseable.
func buildSlotCount() int {
	raw := strings.TrimSpace(os.Getenv(buildSlotsEnv))
	if raw == "" {
		return defaultBuildSlots
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultBuildSlots
	}
	if n < 1 {
		return 1
	}
	return n
}

// resolveTargetDir resolves the directory a cargo build launched at
// workspaceRoot will actually write artifacts to: CARGO_TARGET_DIR when the
// caller set one (that is where the bytes land, whatever the workspace is),
// else <workspace root>/target -- via cargoWorkspaceRoot, so a member crate
// keys on the ONE target its workspace shares rather than a per-crate
// directory that never exists. env is the environment lookup (os.Getenv in
// production).
func resolveTargetDir(env func(string) string, workspaceRoot string) string {
	if explicit := strings.TrimSpace(env("CARGO_TARGET_DIR")); explicit != "" {
		if abs, err := filepath.Abs(explicit); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(explicit)
	}
	ws := cargoWorkspaceRoot(workspaceRoot)
	if dir := cargoConfigTargetDir(ws); dir != "" {
		return dir
	}
	return filepath.Join(ws, "target")
}

// ResolveCargoTargetDir resolves the target dir a cargo invocation run from
// dir writes to. Exported so internal/cli's cargo shim keys its lock
// IDENTICALLY to the hooks/gates -- a direct `cargo build` and a gate's
// build into the same target must contend, and into different targets must
// not.
func ResolveCargoTargetDir(dir string) string {
	return resolveTargetDir(os.Getenv, dir)
}

// runnerTargetDir is the target dir a cargo Runner's build actually writes
// to: resolved from where the command RUNS (Runner.Dir, the cargo workspace
// root, when set), not from the crate root the gate keys its state on.
func runnerTargetDir(r Runner, root string) string {
	dir := root
	if r.Dir != "" {
		dir = r.Dir
	}
	return resolveTargetDir(os.Getenv, dir)
}

// targetDirKey is the stable short key naming a target dir in a lock file
// name: sha256/8 of the cleaned absolute path, case-folded on Windows where
// one directory routinely appears under two drive-letter casings (two keys
// for one directory would admit 2N concurrent builds into it).
func targetDirKey(targetDir string) string {
	clean := filepath.Clean(targetDir)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	sum := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(sum[:8])
}

// targetLockPath is the one lock file for a target dir: .aphrollo/build.lock
// INSIDE the target dir, so every process building into that directory names
// the same file whatever account it runs under. Under a test's lock-dir
// override it keeps the old flat, hashed layout, because a test names target
// dirs that do not exist and must not be created.
func targetLockPath(targetDir string) string {
	if lockDirOverridden() {
		base := strings.TrimSuffix(effectiveBuildLockPath(), ".lock")
		return fmt.Sprintf("%s.%s.lock", base, targetDirKey(targetDir))
	}
	return sharedTargetLockPath(targetDir)
}

// globalSlotPath is the i-th global slot file, beside the target locks:
// "aphrollo-cargo-slot.<i>.lock" in production, and a test-local equivalent
// under an override.
func globalSlotPath(i int) string {
	base := effectiveBuildLockPath()
	name := strings.TrimSuffix(filepath.Base(base), ".lock")
	name = strings.TrimSuffix(name, "-build") + "-slot"
	return filepath.Join(filepath.Dir(base), fmt.Sprintf("%s.%d.lock", name, i))
}

// TryAcquireBuildSlot attempts, ONCE and non-blocking, to take BOTH locks a
// build needs: this target dir's exclusive lock, then any free global slot.
// ok=false means another build owns this target dir, or the box is at
// capacity. Exported so internal/cli's cargo shim can build its own wait
// loop with its own queued/acquired messaging.
func TryAcquireBuildSlot(targetDir, cmd, cwd string) (BuildSlot, func(), bool) {
	lock := targetLockPath(targetDir)
	releaseTarget, ok := TryAcquireFileLock(lock)
	if !ok {
		return BuildSlot{}, func() {}, false
	}
	n := buildSlotCount()
	jobs := slotJobs(totalCargoJobs(), n)
	// A child of a long verb builds under the slot its PARENT already holds.
	// Measured: `cargo mutants` runs four jobs in parallel, each invoking
	// cargo through the shim, so one mutation run took every slot on the box
	// for hours. The per-target lock still applies — a token is permission to
	// skip the semaphore, never permission to share a build directory.
	if token := inheritedSlotToken(); token != "" {
		slot := BuildSlot{Index: inheritedSlotIndex, Lock: lock, Owner: lock + ".owner", Jobs: jobs}
		recordSlotOwner(slot, cmd, cwd)
		return slot, func() {
			clearSlotOwner(slot)
			releaseTarget()
		}, true
	}
	for i := range n {
		if releaseSlot, ok := TryAcquireFileLock(globalSlotPath(i)); ok {
			slot := BuildSlot{Index: i, Lock: lock, Owner: lock + ".owner", Jobs: jobs}
			recordSlotOwner(slot, cmd, cwd)
			return slot, func() {
				clearSlotOwner(slot)
				releaseSlot()
				releaseTarget()
			}, true
		}
	}
	// The box is full: give the target lock straight back, or a busy box
	// would leave every target dir locked by a build that never started.
	releaseTarget()
	return BuildSlot{}, func() {}, false
}

// slotTokenEnv names the global slot a long-running verb is holding, so the
// cargo invocations it spawns can build without taking one of their own.
const slotTokenEnv = "APHROLLO_SLOT_TOKEN"

// inheritedSlotIndex marks a slot that was inherited rather than acquired, so
// nothing tries to account for it as one of the box's N.
const inheritedSlotIndex = -1

// inheritedSlotToken is the parent slot this process was handed, "" when it
// must take its own.
func inheritedSlotToken() string {
	return strings.TrimSpace(os.Getenv(slotTokenEnv))
}

// SlotTokenEnvEntry is the environment entry a long verb passes to its
// children. Exported for the shim, which builds the child environment.
func SlotTokenEnvEntry(s BuildSlot) string {
	return slotTokenEnv + "=" + s.Lock
}

// SlotTokenEnvName is the variable to STRIP from a child that must not
// inherit the token — a launched `cargo run` process outlives the slot
// entirely.
func SlotTokenEnvName() string { return slotTokenEnv }

// TryAcquireLongVerbSlot takes both locks for a long-running verb and hands
// back their releases SEPARATELY: the caller's target lock covers the prewarm
// compile only, while the global slot is held for the whole run and lent to
// the children through the token.
func TryAcquireLongVerbSlot(targetDir, cmd, cwd string) (slot BuildSlot, releaseTarget, releaseAll func(), ok bool) {
	lock := targetLockPath(targetDir)
	freeTarget, ok := TryAcquireFileLock(lock)
	if !ok {
		return BuildSlot{}, func() {}, func() {}, false
	}
	n := buildSlotCount()
	jobs := slotJobs(totalCargoJobs(), n)
	for i := range n {
		freeSlot, got := TryAcquireFileLock(globalSlotPath(i))
		if !got {
			continue
		}
		acquired := BuildSlot{Index: i, Lock: lock, Owner: lock + ".owner", Jobs: jobs}
		recordSlotOwner(acquired, cmd, cwd)
		var once bool
		releaseTargetOnce := func() {
			if !once {
				once = true
				// The TARGET record goes with the target lock — another build
				// may take that dir immediately. The SLOT record stays: the
				// long verb holds the slot for hours, and a waiter blocked on
				// capacity reads exactly that.
				removeBuildLockOwnerAt(acquired.Owner)
				freeTarget()
			}
		}
		return acquired,
			releaseTargetOnce,
			func() {
				releaseTargetOnce()
				removeBuildLockOwnerAt(globalSlotOwnerPath(acquired.Index))
				freeSlot()
			}, true
	}
	freeTarget()
	return BuildSlot{}, func() {}, func() {}, false
}

// buildLockQueueNoticeEvery is how often a WAITING acquirer says who it is
// waiting for. A gate willing to wait twenty minutes must not do it in
// silence -- but it must not repeat itself every poll either. A `var` so a
// test can shrink it.
var buildLockQueueNoticeEvery = 60 * time.Second

// acquireBuildSlot polls for both locks until they are held or deadline
// elapses, announcing the holder every buildLockQueueNoticeEvery while it
// waits. A ZERO deadline is a single try -- the PostToolUse edit hook's
// contract: an edit-time run reports QUEUED-SKIPPED instantly rather than
// spending its budget waiting.
func acquireBuildSlot(targetDir string, deadline time.Duration, cmd, cwd string) (BuildSlot, func(), bool) {
	start := time.Now()
	nextNotice := buildLockQueueNoticeEvery
	for {
		if slot, release, ok := TryAcquireBuildSlot(targetDir, cmd, cwd); ok {
			return slot, release, true
		}
		waited := time.Since(start)
		if waited >= deadline {
			return BuildSlot{}, func() {}, false
		}
		if waited >= nextNotice {
			fmt.Fprintf(os.Stderr, "gate: queued behind %s for %s (waited %.0fs)\n",
				buildSlotHolderDescription(targetDir), targetDir, waited.Seconds())
			nextNotice += buildLockQueueNoticeEvery
		}
		time.Sleep(buildLockPollInterval)
	}
}

// buildSlotHolderDescription names the current holder of a target dir for a
// waiting acquirer's line, or says the holder is unknown (the owner file is
// best-effort, and a build started before this feature wrote none).
func buildSlotHolderDescription(targetDir string) string {
	if o, ok := ReadBuildSlotOwner(targetDir); ok {
		return describeOwner(o)
	}
	// The target dir itself is free, so the wait is on CAPACITY: every global
	// slot is taken by builds in other target dirs. Those are nameable too,
	// and saying "holder unknown" about a build whose record is right there is
	// what sent an operator looking for a phantom.
	for i := range buildSlotCount() {
		if o, ok := readBuildLockOwnerAt(globalSlotOwnerPath(i)); ok {
			return describeOwner(o) + " (the box is at capacity)"
		}
	}
	return "another build (holder unknown)"
}

func describeOwner(o BuildLockOwner) string {
	return fmt.Sprintf("%q in %s (pid %d)", o.Cmd, o.Cwd, o.PID)
}

// globalSlotOwnerPath is the owner record beside the i-th global slot lock.
func globalSlotOwnerPath(i int) string { return globalSlotPath(i) + ".owner" }

// recordSlotOwner is the ONE place an acquisition becomes visible: it writes
// both the target-dir record (who is building HERE) and the global-slot
// record (who is using up the box's capacity). Acquiring and recording are
// one operation because a caller asked to remember a second call eventually
// forgets, and the cost of forgetting is a waiter that cannot name what it is
// waiting for.
func recordSlotOwner(s BuildSlot, cmd, cwd string) {
	writeBuildLockOwnerAt(s.Owner, cmd, cwd)
	writeBuildLockOwnerAt(globalSlotOwnerPath(s.Index), cmd, cwd)
}

func clearSlotOwner(s BuildSlot) {
	removeBuildLockOwnerAt(s.Owner)
	removeBuildLockOwnerAt(globalSlotOwnerPath(s.Index))
}

// ReadBuildSlotOwner names the build currently holding targetDir. ok=false
// means no owner was recorded (holder unknown), never an error -- the owner
// file is best-effort and inherently racy.
func ReadBuildSlotOwner(targetDir string) (BuildLockOwner, bool) {
	return readBuildLockOwnerAt(ReadBuildSlotOwnerPath(targetDir))
}

// ReadBuildSlotOwnerPath is where a target dir's owner record lives, beside
// its lock. Exported so a test can assert every lock artefact lands in the
// overridden lock dir.
func ReadBuildSlotOwnerPath(targetDir string) string {
	return targetLockPath(targetDir) + ".owner"
}

// slotJobs is one slot's share of the box's job budget, never below 1
// (cargo rejects --jobs 0). Integer division deliberately rounds DOWN: the
// cap exists to keep N concurrent link waves inside memory, so the residue
// stays unspent.
func slotJobs(total, slots int) int {
	if slots < 1 {
		slots = 1
	}
	if n := total / slots; n >= 1 {
		return n
	}
	return 1
}

// totalCargoJobs is the box's whole job budget: the operator's own
// ~/.cargo/config.toml `[build] jobs` when they set one (they picked it for
// this machine's memory, and it is the number the old single-lock world
// actually built with), else one job per core.
func totalCargoJobs() int {
	if n, ok := cargoConfigJobs(cargoConfigPath()); ok {
		return n
	}
	return runtime.NumCPU()
}

// cargoConfigPath is the user-level cargo config: $CARGO_HOME/config.toml,
// else ~/.cargo/config.toml. "" when neither can be resolved.
func cargoConfigPath() string {
	home := os.Getenv("CARGO_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".cargo")
	}
	return filepath.Join(home, "config.toml")
}

// cargoConfigJobs reads `[build] jobs = N` from a cargo config file. A line
// scanner suffices for the same reason cargoPackageName's does: the key
// sits directly under its table in any real config, and a parse miss costs
// only the NumCPU fallback -- never a wrong answer, since a `jobs` key
// under another table and a commented-out one both read as absent.
func cargoConfigJobs(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	inBuild := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inBuild = strings.HasPrefix(trimmed, "[build]")
			continue
		}
		if !inBuild {
			continue
		}
		key, val, found := strings.Cut(trimmed, "=")
		if !found || strings.TrimSpace(key) != "jobs" {
			continue
		}
		val, _, _ = strings.Cut(val, "#")
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n < 1 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// EnvWithBuildJobs returns env with CARGO_BUILD_JOBS set to jobs, unless the
// caller already set it: the split is a default for un-tuned sessions, never
// an override of a session that divided the box itself.
func EnvWithBuildJobs(env []string, jobs int) []string {
	// The STRICTER of the two wins. Yielding to a caller's value disengaged
	// the governor entirely: lane shells export CARGO_BUILD_JOBS, and N slots
	// each linking with the whole box's job count is the OOM the cap exists
	// to prevent.
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k != buildJobsEnv {
			out = append(out, kv)
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 && n < jobs {
			jobs = n
		}
	}
	return append(out, fmt.Sprintf("%s=%d", buildJobsEnv, jobs))
}

// buildJobsEnv is cargo's own parallelism knob -- the mechanism by which a
// slot's share of the box is actually enforced on the child build.
const buildJobsEnv = "CARGO_BUILD_JOBS"

// setBuildJobs sets CARGO_BUILD_JOBS in THIS process's environment (which
// RunSuite's suiteEnv inherits into the child) unless the caller already set
// one, and returns the restore. One hook process handles many roots, so the
// environment must go back exactly as it was.
func setBuildJobs(jobs int) (restore func()) {
	if raw, had := os.LookupEnv(buildJobsEnv); had {
		// Same rule as EnvWithBuildJobs: keep the stricter number, never the
		// caller's larger one.
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 && n <= jobs {
			return func() {}
		}
		os.Setenv(buildJobsEnv, strconv.Itoa(jobs))
		return func() { os.Setenv(buildJobsEnv, raw) }
	}
	os.Setenv(buildJobsEnv, strconv.Itoa(jobs))
	return func() { os.Unsetenv(buildJobsEnv) }
}
