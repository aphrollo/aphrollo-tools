package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

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
		// The per-target lock lives in a .aphrollo directory inside the target
		// dir, which the first builder of a fresh target creates. World
		// permissions, because the next builder may be another account.
		if os.IsNotExist(err) {
			if mkErr := ensureSharedSubdir(filepath.Dir(path)); mkErr == nil {
				f, err = openLockFile(path)
			}
		}
	}
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
