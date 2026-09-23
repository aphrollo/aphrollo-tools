//go:build unix

package core

import (
	"os"

	"golang.org/x/sys/unix"
)

// openLockFile opens (creating if needed) the build lock file for locking.
// The mode is world read/write, and set AGAIN after the open because the
// process umask strips it on creation: these locks are shared across accounts,
// and a lock file a second user cannot even open reads as HELD forever (see
// reportLockOpenFailure — it fails closed on purpose).
func openLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, sharedLockFileMode)
	if err != nil {
		return nil, err
	}
	if fi, serr := f.Stat(); serr == nil && fi.Mode().Perm() != sharedLockFileMode {
		// Another account's lock file is not ours to re-mode; the failure is
		// ignored for exactly that case.
		_ = f.Chmod(sharedLockFileMode)
	}
	return f, nil
}

// tryLockExclusive attempts a NON-BLOCKING exclusive flock on f: returns
// immediately with true on success, false if another process already holds
// it (LOCK_EX|LOCK_NB — EWOULDBLOCK means contended, not an error worth
// surfacing). flock is per-OPEN-FILE-DESCRIPTION, so f.Close() (via
// release()) always drops it even if the owning process never calls
// unlockFile — same contract as the Windows side.
func tryLockExclusive(f *os.File) bool {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	return err == nil
}

// unlockFile releases the lock tryLockExclusive took, best-effort — f.Close()
// (by the caller, immediately after) releases it regardless.
func unlockFile(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
