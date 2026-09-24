//go:build unix

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenLockFile_CreatesAtTheSharedModeAndFixesAnExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")

	// Pre-create the file with a mode that does NOT match sharedLockFileMode
	// (0o666): openLockFile must re-mode it, because a lock file another
	// account cannot open reads as HELD forever.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := openLockFile(path)
	if err != nil {
		t.Fatalf("openLockFile: %v", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != sharedLockFileMode {
		t.Fatalf("mode = %o, want %o", fi.Mode().Perm(), sharedLockFileMode)
	}

	// Calling it again on the already-correct file must not error.
	f2, err := openLockFile(path)
	if err != nil {
		t.Fatalf("second openLockFile: %v", err)
	}
	f2.Close()
}

func TestOpenLockFile_CreatesTheFileWhenAbsent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-lock")
	f, err := openLockFile(path)
	if err != nil {
		t.Fatalf("openLockFile on an absent file: %v", err)
	}
	defer f.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should now exist: %v", err)
	}
}

func TestTryLockExclusive_NonBlockingAndReleasable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "flock-target")
	holder, err := openLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()

	if !tryLockExclusive(holder) {
		t.Fatal("first exclusive lock on an unheld file should succeed")
	}

	contender, err := openLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()

	if tryLockExclusive(contender) {
		t.Fatal("a second fd should not acquire the lock while the first holds it")
	}

	unlockFile(holder)

	if !tryLockExclusive(contender) {
		t.Fatal("after unlockFile, the contender should be able to acquire the lock")
	}
	unlockFile(contender)
}
