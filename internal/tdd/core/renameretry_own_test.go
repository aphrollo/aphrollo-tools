package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var errBusy = errors.New("busy")

func TestRetryRename_RetriesARetryableErrorUntilItClears(t *testing.T) {
	calls := 0
	err := retryRename(func() error {
		calls++
		if calls <= 3 {
			return errBusy
		}
		return nil
	}, func(error) bool { return true }, 5*time.Second)
	if err != nil || calls != 4 {
		t.Fatalf("err = %v after %d calls, want nil after 4", err, calls)
	}
}

func TestRetryRename_ReturnsAnErrorNothingWillClearAtOnce(t *testing.T) {
	calls := 0
	err := retryRename(func() error { calls++; return errBusy }, func(error) bool { return false }, 5*time.Second)
	if !errors.Is(err, errBusy) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want busy after 1", err, calls)
	}
}

func TestRetryRename_GivesUpAtTheBoundWithTheLastError(t *testing.T) {
	calls := 0
	start := time.Now()
	err := retryRename(func() error { calls++; return errBusy }, func(error) bool { return true }, 50*time.Millisecond)
	if !errors.Is(err, errBusy) {
		t.Fatalf("err = %v, want the last error", err)
	}
	if calls < 2 {
		t.Errorf("%d call(s): a retryable error must be tried again", calls)
	}
	// The bound is 50 ms; a box this loaded still ends it well inside 10 s.
	if took := time.Since(start); took < 50*time.Millisecond || took > 10*time.Second {
		t.Errorf("gave up after %v, want about the 50ms bound", took)
	}
}

// A write that cannot publish leaves no temp file beside the destination.
func TestWriteFileAtomic_ARenameThatFailsLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "result.json")
	// A directory with a file in it cannot be replaced by a file, on any platform.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(dest, []byte("data")); err == nil {
		t.Fatal("replacing a non-empty directory with a file succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d entries left beside the destination, want only the directory", len(entries))
	}
}
