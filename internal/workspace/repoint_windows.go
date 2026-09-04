//go:build windows

package workspace

import "os"

// repointSymlink replaces symlink with one pointing at target.
//
// Windows has no atomic replace for a directory symlink: MoveFileEx refuses
// outright when either the source or the destination names a directory
// ("This value cannot be used if lpNewFileName or lpExistingFileName names a
// directory" — MSDN), and a directory symlink's reparse point carries
// FILE_ATTRIBUTE_DIRECTORY, so the temp-link-then-rename dance the POSIX
// build uses always fails here with ERROR_ACCESS_DENIED. The honest fix is
// remove-then-create, which drops atomicity: there is a brief window,
// between the Remove and the Symlink below, where the link does not exist at
// all. A reader (a dev unit resolving its WorkingDirectory through this
// link) hitting exactly that window sees "not found" rather than the old or
// the new target; callers that care about a concurrent reader on Windows
// must accept or guard against that window themselves.
//
// os.Remove on a directory-typed reparse point deletes only the reparse
// point entry — Windows RemoveDirectory (which os.Remove calls for a
// directory) never follows a reparse point into what it targets, so the
// directory repointSymlink is replacing FROM, and its contents, are left
// untouched.
func repointSymlink(symlink, target string) error {
	if err := os.Remove(symlink); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, symlink)
}
