package tdd

import (
	"fmt"
	"sync"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// buildLockPath is the well-known machine-wide advisory lock file every
// aphrollo CARGO suite run contends for, so several concurrent Claude
// sessions on one box never thrash the same many-core cargo build in
// parallel (5 sessions each cold-building a Bevy workspace at once was the
// observed failure mode this fixes). Same idea, one level up, as borld's
// crates/server/src/portlock.rs: that lock serialises port binds within ONE
// repo's test suite; this one serialises cargo BUILDS across every
// repo/session on the machine, regardless of which repo each is in.
func buildLockPath() string {
	return filepath.Join(lockDir(), "aphrollo-cargo-build.lock")
}

// lockDirOverride redirects EVERY lock file this package creates — target
// locks, global slots, owner records — into one directory. It is the single
// seam a test isolates: a per-path override is one a test can forget, and
// forgetting left 871 stale lock files in the operator's real %TEMP%.
var lockDirOverride atomic.Pointer[string]

// lockDir is where every aphrollo lock file lives: the machine-wide temp dir
// in production, an overridden directory under test.
func lockDir() string {
	if p := lockDirOverride.Load(); p != nil && *p != "" {
		return *p
	}
	return os.TempDir()
}

// SetLockDirForTest points every lock file at dir for the duration of a test
// (or a package's whole run, from TestMain). Exported because internal/cli
// exercises the same locks through the shims.
func SetLockDirForTest(dir string) (restore func()) {
	prev := lockDirOverride.Swap(&dir)
	return func() { lockDirOverride.Store(prev) }
}

// effectiveBuildLockPath resolves the base lock path every other lock file
// is named from. There is exactly ONE isolation seam behind it — the lock
// DIR — because two overlapping overrides let a test set the one that does
// not cover the paths it touches and write into the real temp dir believing
// it was isolated.
func effectiveBuildLockPath() string {
	return buildLockPath()
}

// setBuildLockPathOverride keeps the old per-path spelling for tests: only
// the DIRECTORY is honoured, since the file names are derived.
func setBuildLockPathOverride(path string) (restore func()) {
	return SetLockDirForTest(filepath.Dir(path))
}

// buildLockPollInterval is how often acquireBuildLock retries while polling
// for the lock. Short — a build-lock wait is meant to be seconds, not the
// portlock's near-instant hand-off, so a coarser interval doesn't cost much
// but keeps CPU spend from polling negligible either way.
const buildLockPollInterval = 20 * time.Millisecond

// buildLockPostEditDeadline bounds how long a PostToolUse edit waits for a
// build slot before giving up. ZERO: a between-edit run is fired dozens of
// times a session inside a 100s budget, and a busy slot means a build that
// is minutes long, so waiting spends the edit's whole test budget to lose
// anyway. One try across the slots, then QUEUED-SKIPPED. A `var`, not a
// `const`, solely so a contention test can lengthen it — production code
// never assigns to it.
var buildLockPostEditDeadline time.Duration

// buildLockPrecommitDeadline bounds how long a commit's cargo stage waits
// for a build slot. Twenty minutes: the gate target dir is one per repo, so
// two lanes committing at once genuinely serialise behind each other's full
// suite — and a wait that expires REJECTS the commit now, so a short budget
// throws away legitimate commits. The acquirer announces the holder once a
// minute while it waits. A `var` for the same test-only reason as
// buildLockPostEditDeadline, and settable by the operator through
// SetPrecommitLockWait.
var buildLockPrecommitDeadline = 1200 * time.Second

// SetPrecommitLockWait overrides how long the commit gate waits for a build
// slot and returns the restore. Exported for internal/cli, which owns every
// operator-facing env knob (APHROLLO_LOCK_WAIT_SECS) so the knobs are
// declared in one place rather than read wherever they happen to be used.
func SetPrecommitLockWait(d time.Duration) (restore func()) {
	prev := buildLockPrecommitDeadline
	buildLockPrecommitDeadline = d
	return func() { buildLockPrecommitDeadline = prev }
}

// runCargoLocked wraps a SuiteRunner invocation with the machine-wide build
// lock: only cargo runners take it (go/pytest/vitest don't saturate the box
// the way a cargo build does), so a non-cargo runner passes straight through,
// untouched, regardless of who holds the lock. The lock is held ONLY for the
// duration of THIS run — acquired immediately before, released immediately
// after — never across stages, and never across a whole Precommit call that
// spans multiple project roots. acquired=false (and a zero-value res) means
// the deadline elapsed before the lock came free; the caller reports that as
// a distinct QUEUED-SKIPPED outcome, never as a timeout (a stopwatch verdict
// on this project's suite) or a failure.
//
// stageBudget is the OVERALL time this call may spend, lock-wait AND suite
// run combined — NOT additional to it. Found in review: with only
// lockDeadline bounding the wait and the injected SuiteRunner carrying its
// OWN separate timeout (e.g. 600s for precommit), a contended lock made the
// two stack additively (300s wait + 600s run = up to 900s per stage, per
// root). r.Deadline is set to start+stageBudget BEFORE the wait begins, so
// by the time run() actually executes, RunSuite sees a Deadline already
// eaten into by however long the wait took, and bounds itself to whichever
// is shorter: its own configured timeout, or the time remaining until
// Deadline.
func runCargoLocked(run SuiteRunner, r Runner, root string, lockDeadline, stageBudget time.Duration) (res SuiteResult, waited time.Duration, acquired bool) {
	if r.Cmd != "cargo" {
		return run(r, root), 0, true
	}
	start := time.Now()
	r.Deadline = start.Add(stageBudget)
	dir := root
	if r.Dir != "" {
		dir = r.Dir
	}
	slot, release, ok := acquireBuildSlot(runnerTargetDir(r, root), lockDeadline)
	waited = time.Since(start)
	if !ok {
		return SuiteResult{}, waited, false
	}
	defer release()

	WriteBuildSlotOwner(slot, cmdString(r), dir)
	defer RemoveBuildSlotOwner(slot)
	defer setBuildJobs(slot.Jobs)()

	// A nested cargo invocation (task A7's cargo-queue shim, IF a session
	// prepended it to PATH) that happens to resolve "cargo" to the shim
	// instead of the real binary must recognize the lock is ALREADY held by
	// THIS process and pass straight through, or it deadlocks on the same
	// lock. suiteEnv() inherits os.Environ(), so setting this in the
	// process's own environment is what actually propagates it to the
	// child.
	prevHeld, hadHeld := os.LookupEnv(BuildLockHeldEnv)
	os.Setenv(BuildLockHeldEnv, "1")
	defer func() {
		if hadHeld {
			os.Setenv(BuildLockHeldEnv, prevHeld)
		} else {
			os.Unsetenv(BuildLockHeldEnv)
		}
	}()

	return run(r, root), waited, true
}

// BuildLockHeldEnv is the environment variable runCargoLocked sets to "1"
// around a cargo invocation it already holds the machine-wide build lock
// for. Exported so internal/cli's `tdd cargo` shim (task A7) can check it
// and pass straight through without touching the lock at all -- without
// this, a session that prepended the shim dir to PATH would deadlock the
// instant a hook/gate's own cargo run (already holding the lock) spawned a
// build-script or similar that itself invokes `cargo` and resolves back
// through the shim.
const BuildLockHeldEnv = "APHROLLO_BUILD_LOCK_HELD"

// GitQueuedEnv is the environment variable the git-queue shim (task A11)
// sets to "1" around a git invocation it already holds the per-repo git
// lock for. Exported so internal/cli's `tdd git` shim can check it (and
// BuildLockHeldEnv too) and pass straight through without touching the
// lock at all -- required because `git commit` (going through the shim,
// holding the lock) fires the pre-commit hook, which is aphrollo ITSELF,
// which spawns its own git subprocesses (worktree add/remove, apply, diff
// --cached, rev-parse, ...) that must never wait on the very lock their own
// parent process currently holds.
const GitQueuedEnv = "APHROLLO_GIT_QUEUED"

// BuildLockOwner records who currently holds the machine-wide cargo build
// lock, written by runCargoLocked (and the cargo shim itself) for the
// duration of the held cargo run, so a WAITING acquirer can name the holder
// instead of reporting a bare "someone else has it".
type BuildLockOwner struct {
	PID       int       `json:"pid"`
	Cwd       string    `json:"cwd"`
	Cmd       string    `json:"cmd"`
	Started   time.Time `json:"started"`
	SessionID string    `json:"session_id,omitempty"`
}

// writeBuildLockOwnerAt records the current process as path's lock holder.
// Best-effort: a write failure never blocks the actual build/git op — it
// only costs a waiting acquirer its ability to NAME the holder. Generalized
// (task A11) so the git-queue shim's per-repo owner file can reuse the
// identical mechanism as the cargo build lock's own owner file.
func writeBuildLockOwnerAt(path, cmd, cwd string) {
	o := BuildLockOwner{
		PID:       os.Getpid(),
		Cwd:       cwd,
		Cmd:       cmd,
		Started:   time.Now().UTC(),
		SessionID: os.Getenv("CLAUDE_SESSION_ID"),
	}
	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// removeBuildLockOwnerAt clears path's owner file. Best-effort, same
// reasoning as writeBuildLockOwnerAt.
func removeBuildLockOwnerAt(path string) {
	_ = os.Remove(path)
}

// WriteFileLockOwner is writeBuildLockOwnerAt, exported so internal/cli's
// shims (cargo, task A7; git, task A11) can record themselves as a lock's
// holder at an ARBITRARY owner-file path once they acquire it — using the
// SAME mechanism runCargoLocked uses for the cargo build lock, so a waiter
// never has to care whether the current holder is a hook/gate or a direct
// shim invocation.
func WriteFileLockOwner(path, cmd, cwd string) { writeBuildLockOwnerAt(path, cmd, cwd) }

// RemoveFileLockOwner is removeBuildLockOwnerAt, exported for the same
// reason as WriteFileLockOwner.
func RemoveFileLockOwner(path string) { removeBuildLockOwnerAt(path) }

// ReadFileLockOwnerAt reads path's owner file, if any. Exported (as
// ReadFileLockOwner) so internal/cli's shims and any future waiter can name
// the holder at an ARBITRARY lock's owner-file path. This is inherently
// racy (the owner file can be removed, or not yet written, at the instant
// of the read) -- ok=false covers all of "no owner recorded", "file
// vanished mid-read", and "corrupt content", and every caller treats that
// as "holder unknown", never an error.
func readBuildLockOwnerAt(path string) (BuildLockOwner, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BuildLockOwner{}, false
	}
	var o BuildLockOwner
	if err := json.Unmarshal(data, &o); err != nil {
		return BuildLockOwner{}, false
	}
	return o, true
}

// ReadFileLockOwner is readBuildLockOwnerAt, exported for the same reason
// as WriteFileLockOwner/RemoveFileLockOwner (task A11's per-repo git lock).
func ReadFileLockOwner(path string) (BuildLockOwner, bool) {
	return readBuildLockOwnerAt(path)
}

// SetBuildLockPathForTest points the build lock (and its paired owner file)
// at an isolated path for the duration of the returned restore call.
// Exported so tests OUTSIDE this package (internal/cli's cargo-shim tests)
// can isolate themselves from the real machine-wide lock the same way this
// package's own withIsolatedBuildLock helper does internally — without it,
// a cross-package test would race the box's own aphrollo PostToolUse hook
// exercising the identical production lock file.
func SetBuildLockPathForTest(path string) (restore func()) {
	return setBuildLockPathOverride(path)
}

// TryAcquireFileLock attempts an exclusive advisory OS file lock at path
// ONCE, non-blocking: (release, true) on success, (no-op, false) on
// contention. The generic primitive TryAcquireBuildLock is built on top of
// -- exposed so ANY other machine/repo-scoped lock (task A11's per-repo git
// lock) can reuse the identical acquire-or-fail-fast contract at an
// ARBITRARY path, not just the well-known cargo build lock. It fails
// CLOSED: a lock file that cannot even be opened (a full disk, a permission
// problem, a delete-pending name on Windows) reports NOT acquired, because
// the opposite answer admits every waiting build at once and lets the sweep
// delete under them.
func TryAcquireFileLock(path string) (release func(), ok bool) {
	f, err := openLockFile(path)
	if err != nil {
		reportLockOpenFailure(path, err)
		return func() {}, false
	}
	if tryLockExclusive(f) {
		return func() {
			unlockFile(f)
			_ = f.Close()
		}, true
	}
	_ = f.Close()
	return func() {}, false
}

// lockOpenFailures keeps the "cannot open a lock file" complaint to one line
// per path per process: the acquire loop polls, and a screenful of identical
// errors buries the one fact that matters.
// bound: one entry per distinct lock path this process touches (a handful).
var lockOpenFailures sync.Map

func reportLockOpenFailure(path string, err error) {
	if _, seen := lockOpenFailures.LoadOrStore(path, true); seen {
		return
	}
	fmt.Fprintf(os.Stderr, "gate: cannot open the build lock %s (%v) — treating it as HELD\n", path, err)
}
