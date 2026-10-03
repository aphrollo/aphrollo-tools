//go:build !windows

package core

import (
	"errors"
	"testing"
)

// A rename over a file another process has open succeeds off Windows, so
// nothing a rename fails with there is worth waiting on.
func TestRenameRetryable_RetriesNothingOffWindows(t *testing.T) {
	if renameRetryable(errors.New("permission denied")) {
		t.Fatal("a failed rename is retried off Windows")
	}
}
