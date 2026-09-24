package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultCargoShimDir is the per-user queue-shim dir `aphrollo install`
// defaults to when nothing overrides it, and the dir the session-start check
// below judges this process's PATH against. It used to be derived from
// bin's own directory unconditionally, which on a box that deploys the
// binary to a system-owned path (this box's CI deploy under
// /opt/aphrollo-cli/releases/<ts>-<sha>/, github-runner-owned) aborted
// install with `mkdir .../cargo-queue: permission denied`; the dir every
// session's PATH is actually configured to prepend
// (~/.local/share/aphrollo/cargo-queue, pinned by hand in aphrollo-infra for
// exactly this reason) is always writable by the account running install.
//
// Windows keeps the existing convention, alongside bin: self-install already
// places the binary under the user's own bin dir (e.g.
// C:/Users/<user>/bin/aphrollo.exe), so the sibling cargo-queue dir is
// already writable and per-user there — the managed CLAUDE.md block's own
// example. goos is a parameter (matching every other OS-conditioned default
// in this codebase, e.g. selfinstall.go's binExtForOS) so a test can prove
// both branches from one machine.
func DefaultCargoShimDir(bin, goos string) string {
	if goos == "windows" {
		return filepath.Join(filepath.Dir(bin), "cargo-queue")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "aphrollo", "cargo-queue")
	}
	return filepath.Join(filepath.Dir(bin), "cargo-queue")
}

// shimBypassLookPathFn resolves a command the way THIS process's PATH
// actually would. A seam so a test can fake resolving outside the shim dir
// without touching the box's own PATH.
var shimBypassLookPathFn = exec.LookPath

// SetShimBypassLookPathForTest overrides the exec.LookPath seam ShimBypassLine
// resolves commands through, returning the restore.
func SetShimBypassLookPathForTest(fn func(name string) (string, error)) (restore func()) {
	prev := shimBypassLookPathFn
	shimBypassLookPathFn = fn
	return func() { shimBypassLookPathFn = prev }
}

// shimBypassLineFn is what HandleSessionStart actually calls, defaulted to
// the real ShimBypassLine. The package's own TestMain points it at a no-op:
// exec.LookPath reads this box's real PATH and DefaultCargoShimDir reads its
// real $HOME, and a box that has the shims installed but is running `go
// test` from a shell that never sourced the profile line prepending them
// (exactly the state this check exists to name) would otherwise make every
// OTHER session-start test's output depend on this host's own setup.
var shimBypassLineFn = ShimBypassLine

// SetShimBypassLineForTest overrides the SessionStart wiring for a test,
// returning the restore.
func SetShimBypassLineForTest(fn func(bin string) string) (restore func()) {
	prev := shimBypassLineFn
	shimBypassLineFn = fn
	return func() { shimBypassLineFn = prev }
}

// shimMarkerName is the file whose presence in a shim dir means the opt-in
// queue shims were actually installed there — written by every install
// regardless of OS (see internal/tdd/install's InstallCargoShim). Its
// absence means the opt-in was never taken, which must stay SILENT: a raw
// git/cargo on PATH is expected there, not a defect.
const shimMarkerName = "cargo"

// shimCommandName is the name exec.LookPath is asked to resolve. Only
// Windows resolves a bare command by extension (matching internal/cli's own
// commandExeNames reasoning).
func shimCommandName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// ShimBypassLine is the SessionStart line naming a `git` or `cargo` that
// resolves OUTSIDE the queue shim dir this box would install into, even
// though the shims ARE installed there — the state a session inherits when
// it started before a PATH change took effect (a shell that never sourced
// the profile line prepending the shim dir, or a fresh aphrollo-infra
// apply): `which cargo` still finds the raw toolchain, so a direct
// invocation never queues behind the build lock, and the primary-checkout
// wall living in the git shim goes unenforced right along with it.
//
// "" when the shim dir holds no shim at all (the opt-in was never taken —
// silence is correct, not a defect) or when both commands already resolve
// inside it.
func ShimBypassLine(bin string) string {
	goos := runtime.GOOS
	shimDir := DefaultCargoShimDir(bin, goos)
	if _, err := os.Stat(filepath.Join(shimDir, shimMarkerName)); err != nil {
		return ""
	}
	var bad []string
	for _, name := range []string{"git", "cargo"} {
		resolved, err := shimBypassLookPathFn(shimCommandName(name, goos))
		if err != nil {
			continue
		}
		if !sameShimDir(filepath.Dir(resolved), shimDir, goos) {
			bad = append(bad, fmt.Sprintf("%s -> %s", name, resolved))
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return fmt.Sprintf("%s do not resolve into the installed queue shim dir %s — start a new shell/session, or run `aphrollo install`",
		strings.Join(bad, ", "), shimDir)
}

// sameShimDir compares two directory paths the way the OS resolves them:
// case-insensitively on Windows, and with separators and trailing slashes
// normalized everywhere. goos is a parameter, like DefaultCargoShimDir's and
// shimCommandName's own, so a test can prove both branches from one machine.
func sameShimDir(a, b, goos string) bool {
	clean := func(p string) string {
		p = filepath.Clean(filepath.FromSlash(p))
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if goos == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	return clean(a) == clean(b)
}
