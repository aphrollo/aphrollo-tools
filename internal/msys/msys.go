// Package msys keeps the MSYS shell tools of a Windows box from taking a
// throwaway directory for their /tmp.
//
// Git Bash's /tmp is the mount "none /tmp usertemp": the user temp directory
// of the FIRST MSYS process of the session, remembered in MSYS's shared memory
// for as long as any MSYS process is alive. A test binary (or a mutation run)
// that points TEMP at a directory of its own and starts a shell or a git hook
// while no other MSYS process is alive hands that directory to every shell
// started after it; when the run removes the directory, every one of them
// prints "bash.exe: warning: could not find /tmp, please create!". Anchor
// starts the first MSYS process itself, with the temp directory the process was
// started with, before the environment is moved.
package msys

import (
	"sync"
	"sync/atomic"
)

var (
	anchorOnce sync.Once
	anchored   atomic.Bool
	// anchorRelease holds the anchor's stdin open: the shell lives until it
	// reads EOF there, which is the process ending or Release.
	anchorRelease = func() {}
)

// Anchor makes sure an MSYS process started under this process's own temp
// directory is alive for the rest of the process's life: it ends when the
// process does, however it ends. It does nothing off Windows or where no sh is
// on the PATH, and it never fails: a box without the shell tools has no /tmp to
// poison.
func Anchor() {
	anchorOnce.Do(func() {
		anchorRelease, _ = startAnchor()
		anchored.Store(true)
	})
}

// Release lets the anchor go, for a caller that knows no run needs it any more.
func Release() {
	anchorRelease()
}

// Anchored reports whether Anchor has run in this process, for the callers
// whose tests state that they moved the temp directory only after it.
func Anchored() bool {
	return anchored.Load()
}
