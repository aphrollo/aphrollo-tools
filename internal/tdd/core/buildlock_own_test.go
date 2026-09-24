package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTryAcquireFileLock_ExclusiveAndReleasable(t *testing.T) {
	t.Parallel()
	// The parent directory does not exist yet, so the first acquire must
	// take the ensureSharedSubdir(os.IsNotExist) branch to create it.
	path := filepath.Join(t.TempDir(), "sub", "lock")

	release, ok := TryAcquireFileLock(path)
	if !ok {
		t.Fatal("first acquire on a fresh lock path should succeed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file should now exist: %v", err)
	}

	if _, ok := TryAcquireFileLock(path); ok {
		t.Fatal("a second acquire while the first is held should fail (contention)")
	}

	release()

	release2, ok := TryAcquireFileLock(path)
	if !ok {
		t.Fatal("acquire after release should succeed")
	}
	release2()
}

func TestTryAcquireFileLock_FailsClosedWhenTheFileCannotBeOpened(t *testing.T) {
	t.Parallel()
	// A lock file whose PARENT is itself a plain file can never be created —
	// ensureSharedSubdir's MkdirAll fails too, so the function must fail
	// closed (not acquired) rather than treat that as free.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "lock")

	release, ok := TryAcquireFileLock(path)
	if ok {
		t.Fatal("acquiring under a file-as-directory path must fail closed")
	}
	release() // must be a safe no-op
}

func TestReportLockOpenFailure_WarnsOncePerPath(t *testing.T) {
	// Uses the package-level dedupe map, so give this test its own path
	// unique to the whole binary run.
	path := filepath.Join(t.TempDir(), "unique-lock-path")

	stderr := captureStderr(t, func() {
		reportLockOpenFailure(path, os.ErrPermission)
		reportLockOpenFailure(path, os.ErrPermission)
	})

	if n := strings.Count(stderr, path); n != 1 {
		t.Fatalf("warning printed %d time(s) for the same path, want 1:\n%s", n, stderr)
	}
	if !strings.Contains(stderr, "treating it as HELD") {
		t.Fatalf("warning missing the fail-closed explanation:\n%s", stderr)
	}
}
