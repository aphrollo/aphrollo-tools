//go:build windows

package tdd

import (
	"os"
	"testing"
	"time"
)

// TestProcessStartTime_ReturnsCreationTimeForTheCallingProcess pins the
// success path: OpenProcess and GetProcessTimes both succeed for our own
// live pid, so the OS's creation timestamp must come back rather than the
// zero-value failure this function returns when either call errors.
func TestProcessStartTime_ReturnsCreationTimeForTheCallingProcess(t *testing.T) {
	t.Parallel()
	got, ok := processStartTime(os.Getpid())

	if !ok {
		t.Fatal("querying our own live pid must succeed")
	}
	if got.IsZero() {
		t.Fatal("a successful query must report a non-zero creation time")
	}
	if got.After(time.Now()) {
		t.Fatalf("creation time %v must not be in the future", got)
	}
}
