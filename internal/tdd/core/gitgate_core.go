package core

import (
	"strings"
)

// shellPath renders a binary path for embedding in a shell command line:
// backslashes become forward slashes UNCONDITIONALLY (filepath.ToSlash is a
// no-op off Windows, but a Windows path must render identically wherever the
// string is generated or tested — same-bytes-out determinism). Windows accepts
// forward slashes natively; a POSIX filename containing a literal backslash is
// not a path this installer ever writes.
func shellPath(bin string) string {
	return strings.ReplaceAll(bin, `\`, "/")
}
