//go:build windows

package core

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// renameRetryable reports whether err is Windows refusing to replace a file
// another process has open: the harvest polls the very path being written, and
// a reader's handle blocks the rename until it closes.
func renameRetryable(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

// replaceFile moves tmp over dst. os.Rename is MoveFileEx, which fails with
// access denied while any handle on dst is open, even one that shares delete;
// a POSIX-semantics rename replaces a file whose readers share delete, leaves
// them reading the old content, and shows no moment in which dst is missing.
// A volume that cannot do that rename (an older Windows, a share) falls back to
// os.Rename, whose refusals renameRetryable still waits out.
func replaceFile(tmp, dst string) error {
	err := posixRename(tmp, dst)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return os.Rename(tmp, dst)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: dst, Err: err}
	}
	return nil
}

// renameInfoEx is FILE_RENAME_INFO of the FileRenameInfoEx class: the flags,
// the target directory handle (unused here), and the target name in UTF-16.
type renameInfoEx struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

func posixRename(tmp, dst string) error {
	src, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	name, err := windows.UTF16FromString(dst)
	if err != nil {
		return err
	}
	name = name[:len(name)-1] // the length excludes the terminating NUL
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(src, windows.DELETE|windows.SYNCHRONIZE, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }() // the rename has landed or failed by now

	nameBytes := 2 * len(name)
	// Words, not bytes, so the struct laid over the buffer is aligned.
	buf := make([]uint64, (int(unsafe.Offsetof(renameInfoEx{}.FileName))+nameBytes+2+7)/8)
	info := (*renameInfoEx)(unsafe.Pointer(&buf[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.FileNameLength = uint32(nameBytes)
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)
	size := uint32(len(buf) * 8)
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, (*byte)(unsafe.Pointer(&buf[0])), size)
}
