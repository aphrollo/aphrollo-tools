//go:build !windows

package tdd

import (
	"os"
	"testing"
	"time"
)

// TestProcessStartTime_ReturnsCreationTimeForTheCallingProcess pins the
// success path: `ps` finds our own live pid and its lstart output parses, so
// the OS's creation timestamp must come back rather than the zero-value
// failure this function returns when the command, its output, or the parse
// fails.
//
// This file cannot be compiled or run on the Windows box this was written
// on (the package only builds deferred_windows.go there); it is verified
// with `GOOS=linux go vet ./internal/tdd/` instead of `go test`.
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
