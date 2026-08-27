package tdd

import (
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
	return filepath.Join(os.TempDir(), "aphrollo-cargo-build.lock")
}

// buildLockPathOverride lets a test point acquireBuildLock at an ISOLATED
// lock file instead of the real machine-wide one. Without this, any test
// exercising the production path races the box's OWN aphrollo PostToolUse
// hook — which runs `go test` against this same working tree after every
// Edit/Write and, once installed, will exercise this exact lock file too —
// producing spurious contention that has nothing to do with the behavior
// under test. "" (the default, and the only value in production) means: use
// the real well-known path. Atomic because `go test` runs packages in
// parallel and SetBuildLockPathForTest is exported for other packages' use.
var buildLockPathOverride atomic.Pointer[string]

// effectiveBuildLockPath resolves the lock path acquireBuildLock actually
// uses: the override when a test has set one, else the production path.
func effectiveBuildLockPath() string {
	if p := buildLockPathOverride.Load(); p != nil && *p != "" {
		return *p
	}
	return buildLockPath()
}

// setBuildLockPathOverride swaps the override and returns a restore func.
// Every read and write goes through the atomic: `go test` runs packages in
// parallel, so one package's restore races another's lock acquire, and CI
// runs -race.
func setBuildLockPathOverride(path string) (restore func()) {
	prev := buildLockPathOverride.Swap(&path)
	return func() { buildLockPathOverride.Store(prev) }
}

// buildLockPollInterval is how often acquireBuildLock retries while polling
// for the lock. Short — a build-lock wait is meant to be seconds, not the
// portlock's near-instant hand-off, so a coarser interval doesn't cost much
// but keeps CPU spend from polling negligible either way.
const buildLockPollInterval = 20 * time.Millisecond

// buildLockPostEditDeadline bounds how long a PostToolUse edit waits for the
// machine-wide cargo build lock before giving up: a between-edit run is
// fired dozens of times a session, so it must fail fast rather than queuing
// behind a build that could easily be minutes long. A `var`, not a `const`,
// solely so a test can shrink it (see withIsolatedBuildLock) — production
// code never assigns to it.
var buildLockPostEditDeadline = 20 * time.Second

// buildLockPrecommitDeadline bounds how long a commit's cargo stage waits for
// the lock: a commit is a deliberate, infrequent action worth waiting
// longer for than an edit — but the wait still must not exceed a big chunk
// of the overall precommit budget (600s in production), leaving room for the
// suite itself once the lock is actually acquired. A `var` for the same
// test-only reason as buildLockPostEditDeadline.
var buildLockPrecommitDeadline = 300 * time.Second

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
	release, ok := acquireBuildLock(lockDeadline)
	waited = time.Since(start)
	if !ok {
		return SuiteResult{}, waited, false
	}
	defer release()

	dir := root
	if r.Dir != "" {
		dir = r.Dir
	}
	writeBuildLockOwner(cmdString(r), dir)
	defer removeBuildLockOwner()

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

// buildLockOwnerPath is the owner file's path, paired 1:1 with whichever
// lock path is currently effective (production or a test's isolated
// override via buildLockPathOverride) so the two never point at mismatched
// locks in a test.
func buildLockOwnerPath() string {
	return effectiveBuildLockPath() + ".owner"
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

func writeBuildLockOwner(cmd, cwd string) {
	writeBuildLockOwnerAt(buildLockOwnerPath(), cmd, cwd)
}

// removeBuildLockOwnerAt clears path's owner file. Best-effort, same
// reasoning as writeBuildLockOwnerAt.
func removeBuildLockOwnerAt(path string) {
	_ = os.Remove(path)
}

func removeBuildLockOwner() {
	removeBuildLockOwnerAt(buildLockOwnerPath())
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

// WriteBuildLockOwner is writeBuildLockOwner, exported so internal/cli's
// `tdd cargo` shim (task A7) can record itself as the lock's holder once it
// acquires — using the SAME owner-file mechanism runCargoLocked uses, so a
// waiter never has to care whether the current holder is a hook/gate or a
// direct shim invocation.
func WriteBuildLockOwner(cmd, cwd string) { writeBuildLockOwner(cmd, cwd) }

// RemoveBuildLockOwner is removeBuildLockOwner, exported for the same reason
// as WriteBuildLockOwner.
func RemoveBuildLockOwner() { removeBuildLockOwner() }

// ReadBuildLockOwner reads the current build-lock owner file, if any.
// Exported so internal/cli's cargo shim (task A7) and any future waiter can
// name the holder. This is inherently racy (the owner file can be removed,
// or not yet written, at the instant of the read) -- ok=false covers all of
// "no owner recorded", "file vanished mid-read", and "corrupt content", and
// every caller treats that as "holder unknown", never an error.
func ReadBuildLockOwner() (BuildLockOwner, bool) {
	return readBuildLockOwnerAt(buildLockOwnerPath())
}

// ReadFileLockOwner is readBuildLockOwnerAt, exported for the same reason
// as WriteFileLockOwner/RemoveFileLockOwner (task A11's per-repo git lock).
func ReadFileLockOwner(path string) (BuildLockOwner, bool) {
	return readBuildLockOwnerAt(path)
}

// acquireBuildLock polls for the exclusive machine-wide cargo build lock
// until either it is acquired (returns a release func and true) or deadline
// elapses (returns a no-op func and false, having waited approximately
// deadline). The underlying OS lock is released when the holding file handle
// closes — via release() OR the owning process dying — so a killed session
// never leaves a stale lock wedging every other one behind it; no PID
// bookkeeping is needed for correctness.
//
// If the lock file itself can't even be opened (a permissions problem, a
// read-only temp dir), acquisition fails OPEN: the caller gets ok=true so a
// build proceeds unlocked rather than every cargo run silently refusing to
// test because of unrelated lock-file plumbing.
// TryAcquireBuildLock attempts the machine-wide cargo build lock ONCE,
// non-blocking: (release, true) on success, (no-op, false) on contention.
// Exported so internal/cli's `tdd cargo` shim (task A7) can build its OWN
// wait loop around it (queued/acquired/give-up messaging is the shim's
// concern, not this package's) — distinct from acquireBuildLock's silent
// poll-with-deadline, which the hooks/gates use directly.
func TryAcquireBuildLock() (release func(), ok bool) {
	return TryAcquireFileLock(effectiveBuildLockPath())
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
// ARBITRARY path, not just the well-known cargo build lock. Same fail-open
// policy as acquireBuildLock: an unopenable lock file (permissions, a
// read-only dir) never blocks the caller.
func TryAcquireFileLock(path string) (release func(), ok bool) {
	f, err := openLockFile(path)
	if err != nil {
		return func() {}, true
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

func acquireBuildLock(deadline time.Duration) (release func(), ok bool) {
	path := effectiveBuildLockPath()
	start := time.Now()
	for {
		if release, ok := TryAcquireFileLock(path); ok {
			return release, true
		}
		if time.Since(start) >= deadline {
			return func() {}, false
		}
		time.Sleep(buildLockPollInterval)
	}
}
