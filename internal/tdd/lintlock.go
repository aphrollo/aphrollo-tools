package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// golangci-lint takes its OWN machine-wide advisory lock
// ($TMPDIR/golangci-lint.lock) before it analyses anything, and
// --allow-serial-runners (already passed, precommit_go.go) makes a SECOND
// instance retry against context.Background() every second rather than give
// up after 5s — verified against gofrs/flock@v0.13.0's tryCtx and
// golangci-lint v2.12.2's runCommand.acquireFileLock (pkg/commands/run.go):
// with the flag set, the retry context carries no deadline at all, so a
// waiter keeps polling until the lock frees. So the flag DOES serialize two
// runs under the SAME account — that was never the actual bug.
//
// What breaks it is gofrs/flock's default file permission: flock.New(path)
// opens the lock file 0600 (flock.go, no WithPermissions option passed), so
// whichever account's golangci-lint creates $TMPDIR/golangci-lint.lock FIRST
// owns it, and every OTHER account's os.OpenFile on that same path fails
// with a permission error — not "already locked". golangci-lint's own
// acquireFileLock discards whatever error TryLockContext returns and always
// reports the identical "parallel golangci-lint is running" text, so a
// same-account contention that would have retried forever and a
// cross-account permission error that will NEVER resolve by waiting are
// indistinguishable from outside. Confirmed live: PR #740's self-hosted
// `lint` job (user github-runner) hit the same message a local commit gate
// (user debian) does, sharing the box's one /tmp.
//
// This lock sidesteps that entirely rather than trying to out-wait an error
// that never clears: it reuses the SAME cross-account primitive the build
// lock already relies on (TryAcquireFileLock, lockDir() — /var/tmp/aphrollo-locks
// in production, 0o666 and re-chmodded on open even when a different account
// created it — see buildlock_unix.go and lockshared.go) so every account on
// the box serializes on ONE lock file before it ever touches golangci-lint.
// With that guaranteed, at most one golangci-lint process runs anywhere on
// the box at a time, so its own internal lock always finds itself free: the
// SAME lock file it always creates, torn down by whoever last held it,
// recreated fresh (and therefore owned) by whoever holds THIS lock next.
//
// It is its own dedicated lock, not the cargo/`-race` build-slot pool
// (buildslots.go, hasRaceFlag): that pool exists to keep N heavy builds from
// oversubscribing CPU/RAM, and a lint pass over a handful of touched
// packages is not that — it needs mutual exclusion, not a share of a job
// budget.

// lintLockPath is the machine-wide advisory lock every aphrollo-triggered
// golangci-lint invocation takes before it runs, whatever account or repo it
// runs from — the local commit gate and the CI `gate lint` wrapper
// (internal/cli) both resolve to this same file via lockDir().
func lintLockPath() string {
	return filepath.Join(lockDir(), "aphrollo-golangci-lint.lock")
}

// lintLockOwnerPath is the owner record beside the lock, read the same way
// every other lock in this package names its current holder.
func lintLockOwnerPath() string {
	return lintLockPath() + ".owner"
}

// lintLockNoticeEvery is how often a queued lint says who it is waiting for.
// A `var`, not a `const`, only so a contention test can shrink it —
// production never assigns to it.
var lintLockNoticeEvery = 60 * time.Second

// lintLockDeadline bounds how long the commit gate waits for another
// gate-triggered lint to finish before refusing the commit outright. Five
// minutes: long enough that a real burst of concurrent lanes queues rather
// than collides, short enough that a genuinely wedged holder does not hang a
// commit all day. A `var` for the same test-only reason as the build lock's
// own deadlines.
var lintLockDeadline = 300 * time.Second

// TryAcquireLintLock attempts, once, non-blocking, to take the box-wide lint
// lock: (release, true) on success, (no-op, false) on contention. Exported
// so internal/cli's `gate lint` wrapper can build its own wait loop with its
// own messaging, the same way TryAcquireBuildSlot lets the cargo shim build
// its own.
func TryAcquireLintLock(cmd, cwd string) (release func(), ok bool) {
	rel, acquired := TryAcquireFileLock(lintLockPath())
	if !acquired {
		return func() {}, false
	}
	writeBuildLockOwnerAt(lintLockOwnerPath(), cmd, cwd)
	return func() {
		removeBuildLockOwnerAt(lintLockOwnerPath())
		rel()
	}, true
}

// AcquireLintLock polls for the box-wide lint lock until it is held or
// deadline elapses, announcing the holder every lintLockNoticeEvery while it
// waits. Exported so internal/cli's `gate lint` wrapper (the entry point
// CI's workflow step calls instead of golangci-lint directly) waits on the
// IDENTICAL lock the local commit gate does.
func AcquireLintLock(cmd, cwd string, deadline time.Duration) (release func(), waited time.Duration, ok bool) {
	start := time.Now()
	nextNotice := lintLockNoticeEvery
	for {
		if rel, acquired := TryAcquireLintLock(cmd, cwd); acquired {
			return rel, time.Since(start), true
		}
		waited = time.Since(start)
		if waited >= deadline {
			return func() {}, waited, false
		}
		if waited >= nextNotice {
			fmt.Fprintf(os.Stderr, "gate: queued behind %s for golangci-lint (waited %.0fs)\n",
				LintLockHolderDescription(), waited.Seconds())
			nextNotice += lintLockNoticeEvery
		}
		time.Sleep(buildLockPollInterval)
	}
}

// LintLockHolderDescription names the current holder of the box-wide lint
// lock for a waiting acquirer's line, or says the holder is unknown — the
// owner file is best-effort and racy by construction, exactly like every
// other lock's own holder description in this package. Exported for the
// same reason AcquireLintLock and TryAcquireLintLock are.
func LintLockHolderDescription() string {
	if o, ok := readBuildLockOwnerAt(lintLockOwnerPath()); ok {
		return describeOwner(o)
	}
	return "another lint run (holder unknown)"
}
