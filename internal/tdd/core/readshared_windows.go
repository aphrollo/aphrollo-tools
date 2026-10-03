//go:build windows

package core

import (
	"os"

	"golang.org/x/sys/windows"
)

// openShared opens path for reading with delete sharing, which os.Open does
// not: Windows refuses to replace a file while a handle without it is open, so
// a reader polling a file that is rewritten by rename would block the writer
// for as long as it loops. The handle keeps reading the content it opened.
func openShared(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(name, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
