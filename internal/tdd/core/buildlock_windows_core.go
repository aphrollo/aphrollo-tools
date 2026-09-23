//go:build windows

package core

import (
	"os"

	"golang.org/x/sys/windows"
)

// openLockFile opens (creating if needed) the build lock file for locking.
// The handle itself carries no lock state until tryLockExclusive succeeds.
// The mode is nominal here — Windows ignores everything but the write bit and
// the file inherits its directory's ACL — but it matches the unix side, where
// a private lock file locks every other account out permanently.
func openLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, sharedLockFileMode)
}

// tryLockExclusive attempts a NON-BLOCKING exclusive lock on f's whole byte
// range via LockFileEx (LOCKFILE_FAIL_IMMEDIATELY): returns immediately with
// true on success, false if another handle already holds it. Windows file
// locks are mandatory and per-HANDLE, so closing f (via release()) always
// drops the lock even if the owning process never calls UnlockFileEx — the
// contract acquireBuildLock's doc comment relies on.
func tryLockExclusive(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), ^uint32(0), &overlapped)
	return err == nil
}

// unlockFile releases the lock tryLockExclusive took, best-effort — f.Close()
// (by the caller, immediately after) releases it regardless, so a failure
// here is not load-bearing.
func unlockFile(f *os.File) {
	h := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(h, 0, ^uint32(0), ^uint32(0), &overlapped)
}
