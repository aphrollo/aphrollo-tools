//go:build windows

package core

import (
	"errors"

	"golang.org/x/sys/windows"
)

// renameRetryable reports whether err is Windows refusing to replace a file
// another process has open: the harvest polls the very path being written, and
// a reader's handle blocks the rename until it closes.
func renameRetryable(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
