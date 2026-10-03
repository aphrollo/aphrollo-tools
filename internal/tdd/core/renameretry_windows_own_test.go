//go:build windows

package core

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// A reader holding the destination open makes MoveFileEx fail with one of
// these two codes, wrapped in the *LinkError os.Rename returns.
func TestRenameRetryable_WindowsRetriesAnOpenReaderAndNothingElse(t *testing.T) {
	link := func(e error) error { return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: e} }
	for name, err := range map[string]error{
		"access denied":     link(windows.ERROR_ACCESS_DENIED),
		"sharing violation": link(windows.ERROR_SHARING_VIOLATION),
	} {
		if !renameRetryable(err) {
			t.Errorf("%s is not retried", name)
		}
	}
	for name, err := range map[string]error{
		"file not found": link(windows.ERROR_FILE_NOT_FOUND),
		"path not found": link(windows.ERROR_PATH_NOT_FOUND),
	} {
		if renameRetryable(err) {
			t.Errorf("%s is retried, but waiting will not bring the file back", name)
		}
	}
}
