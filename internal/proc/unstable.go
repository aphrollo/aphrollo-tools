package proc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnstableBinary is what a writer of a hook or shim answers for a binary
// that lives somewhere the box does not keep: a scratch or temp dir, a go-build
// dir, a worktree's build output. Such a path is wired once and vanishes later,
// and every gated git call after that runs ungated.
var ErrUnstableBinary = errors.New("the binary is in a temporary or build location that will not last")

// IsUnstableBinary reports whether path sits in a location a hook, shim or
// settings entry must not point at by default: under the OS temp dir (or /tmp,
// /var/tmp), inside a go-build dir, or inside a `.worktrees` tree. It judges
// the location only; a caller that was told the path explicitly does not ask.
func IsUnstableBinary(path string) bool {
	slash := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(path, `\`, "/")))
	for _, part := range strings.Split(slash, "/") {
		if strings.HasPrefix(part, "go-build") || part == ".worktrees" {
			return true
		}
	}
	// goos-ok: the POSIX temp roots are named beside os.TempDir so a TMPDIR override never hides them
	for _, root := range []string{os.TempDir(), "/tmp", "/var/tmp"} {
		dir := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root)), "/")
		if strings.HasPrefix(slash, dir+"/") {
			return true
		}
	}
	return false
}
