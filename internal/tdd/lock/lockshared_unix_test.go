//go:build unix

package lock

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTryAcquireFileLock_LeavesTheLockFileWritableByEveryAccount is the
// permission half of sharing a lock, and it is a unix statement: Windows lock
// files inherit the directory ACL and carry no meaningful mode bits. A lock
// file created 0600 cannot even be OPENED by a second account, and an
// unopenable lock reads as HELD (it fails closed, deliberately) — so a shared
// lock created private is a lock that blocks every other user forever.
func TestTryAcquireFileLock_LeavesTheLockFileWritableByEveryAccount(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "shared.lock")
	release, ok := TryAcquireFileLock(path)
	if !ok {
		t.Fatal("could not take a fresh lock")
	}
	release()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o006 != 0o006 {
		t.Fatalf("lock file mode is %04o, want another account able to read and write it", fi.Mode().Perm())
	}
}
