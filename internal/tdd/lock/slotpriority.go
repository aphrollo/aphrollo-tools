package lock

import (
	"os"
	"path/filepath"
	"strings"
)

// A full-tree build of a Bevy-sized workspace holds its target dir for
// minutes; a per-crate build for the source edit a session is waiting on
// holds it for seconds. When both are queued for one target dir and the slot
// frees, whichever polls first took it, so a full-tree build could run ahead
// of a per-crate one that then gave up with its code untested (issue #830).
//
// So a queued full-tree request does not try for the slot while a live
// package-scoped request is queued for the same target dir: the per-crate
// build goes first, and the full-tree build tries again once no such request
// is waiting. Only the order among waiters changes. A build that already
// holds the slot runs to the end, and a full-tree request that keeps
// yielding past its own deadline ends timed out, as it would behind any
// holder.

// cargoPackageScoped reports whether a cargo command names its packages
// (`-p`, `--package`, `--package=`), which makes it a per-crate build rather
// than a build of the whole workspace.
func cargoPackageScoped(cmd string) bool {
	for _, f := range strings.Fields(cmd) {
		if f == "-p" || f == "--package" || strings.HasPrefix(f, "--package=") {
			return true
		}
	}
	return false
}

// packageRequestQueued reports whether a live package-scoped request is
// queued for target. An unreadable queue dir lists no request.
func packageRequestQueued(target string) bool {
	entries, _ := os.ReadDir(slotQueueDir())
	prefix := targetDirKey(target) + "."
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		r, ok := readSlotRequest(filepath.Join(slotQueueDir(), e.Name()))
		if ok && r.Scoped && pidRunningFn(r.PID) {
			return true
		}
	}
	return false
}
