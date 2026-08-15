package tdd

import (
	"os"
	"path/filepath"
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
// the real well-known path.
var buildLockPathOverride string

// effectiveBuildLockPath resolves the lock path acquireBuildLock actually
// uses: the override when a test has set one, else the production path.
func effectiveBuildLockPath() string {
	if buildLockPathOverride != "" {
		return buildLockPathOverride
	}
	return buildLockPath()
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
func runCargoLocked(run SuiteRunner, r Runner, root string, lockDeadline time.Duration) (res SuiteResult, waited time.Duration, acquired bool) {
	if r.Cmd != "cargo" {
		return run(r, root), 0, true
	}
	start := time.Now()
	release, ok := acquireBuildLock(lockDeadline)
	waited = time.Since(start)
	if !ok {
		return SuiteResult{}, waited, false
	}
	defer release()
	return run(r, root), waited, true
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
func acquireBuildLock(deadline time.Duration) (release func(), ok bool) {
	path := effectiveBuildLockPath()
	f, err := openLockFile(path)
	if err != nil {
		return func() {}, true
	}
	start := time.Now()
	for {
		if tryLockExclusive(f) {
			return func() {
				unlockFile(f)
				_ = f.Close()
			}, true
		}
		if time.Since(start) >= deadline {
			_ = f.Close()
			return func() {}, false
		}
		time.Sleep(buildLockPollInterval)
	}
}
