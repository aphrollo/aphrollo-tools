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

// The cargo build lock is an OOM/CPU governor, not a correctness device --
// cargo already serialises builds WITHIN one target dir via its own
// build-dir flock. So the lock is keyed on the TARGET DIR a build writes to
// (two worktrees with separate targets never compete for anything) and
// admits N concurrent holders per key, each capped to 1/N of the box's job
// budget so N builds cost about what one uncapped build used to.

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
	return filepath.Join(cargoWorkspaceRoot(workspaceRoot), "target")
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

// buildSlotLockPath is the lock file for one slot of one target dir:
// "<base>.<key>.<slot>.lock", derived from the effective base path so a
// test's isolated override (SetBuildLockPathForTest) keys its own family of
// slot files instead of the machine-wide ones.
func buildSlotLockPath(targetDir string, slot int) string {
	base := strings.TrimSuffix(effectiveBuildLockPath(), ".lock")
	return fmt.Sprintf("%s.%s.%d.lock", base, targetDirKey(targetDir), slot)
}

// TryAcquireBuildSlot attempts, ONCE and non-blocking, to take any free slot
// of targetDir's key, trying slots in index order so a box with idle
// capacity always fills slot 0 first (an operator reading %TEMP% sees the
// low indices in use, not a scatter). ok=false means every slot is busy.
// Exported so internal/cli's cargo shim can build its own wait loop with
// its own queued/acquired messaging.
func TryAcquireBuildSlot(targetDir string) (BuildSlot, func(), bool) {
	n := buildSlotCount()
	jobs := slotJobs(totalCargoJobs(), n)
	for i := range n {
		lock := buildSlotLockPath(targetDir, i)
		if release, ok := TryAcquireFileLock(lock); ok {
			return BuildSlot{Index: i, Lock: lock, Owner: lock + ".owner", Jobs: jobs}, release, true
		}
	}
	return BuildSlot{}, func() {}, false
}

// acquireBuildSlot polls for a free slot of targetDir until one is acquired
// or deadline elapses. A ZERO deadline is a single try across the slots --
// the PostToolUse edit hook's contract: an edit-time run must report
// QUEUED-SKIPPED instantly rather than spend its budget waiting.
func acquireBuildSlot(targetDir string, deadline time.Duration) (BuildSlot, func(), bool) {
	start := time.Now()
	for {
		if slot, release, ok := TryAcquireBuildSlot(targetDir); ok {
			return slot, release, true
		}
		if time.Since(start) >= deadline {
			return BuildSlot{}, func() {}, false
		}
		time.Sleep(buildLockPollInterval)
	}
}

// ReadBuildSlotOwner names a holder of targetDir's build slots: the first
// slot with a readable owner file. With every slot busy any of them is a
// truthful answer to "who is building here" -- the message it feeds says
// "queued behind", not "the only holder". ok=false means no slot recorded
// an owner (holder unknown), never an error.
func ReadBuildSlotOwner(targetDir string) (BuildLockOwner, bool) {
	for i := range buildSlotCount() {
		if o, ok := readBuildLockOwnerAt(buildSlotLockPath(targetDir, i) + ".owner"); ok {
			return o, true
		}
	}
	return BuildLockOwner{}, false
}

// WriteBuildSlotOwner / RemoveBuildSlotOwner record and clear the holder of
// an acquired slot. Exported for internal/cli's shim, which holds slots
// directly rather than through runCargoLocked.
func WriteBuildSlotOwner(s BuildSlot, cmd, cwd string) { writeBuildLockOwnerAt(s.Owner, cmd, cwd) }

func RemoveBuildSlotOwner(s BuildSlot) { removeBuildLockOwnerAt(s.Owner) }

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
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == buildJobsEnv {
			return env
		}
	}
	return append(env, fmt.Sprintf("%s=%d", buildJobsEnv, jobs))
}

// buildJobsEnv is cargo's own parallelism knob -- the mechanism by which a
// slot's share of the box is actually enforced on the child build.
const buildJobsEnv = "CARGO_BUILD_JOBS"

// setBuildJobs sets CARGO_BUILD_JOBS in THIS process's environment (which
// RunSuite's suiteEnv inherits into the child) unless the caller already set
// one, and returns the restore. One hook process handles many roots, so the
// environment must go back exactly as it was.
func setBuildJobs(jobs int) (restore func()) {
	if _, had := os.LookupEnv(buildJobsEnv); had {
		return func() {}
	}
	os.Setenv(buildJobsEnv, strconv.Itoa(jobs))
	return func() { os.Unsetenv(buildJobsEnv) }
}
