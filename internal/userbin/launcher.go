package userbin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// LauncherPath is the file a person types `aphrollo` through: it follows the
// pointer exactly as a hook does, so an update reaches the shell at once.
func LauncherPath(root string) string { return filepath.Join(root, launcherName) }

// WriteLauncher writes the launchers under root, falling back to fallback the
// way the hooks do, and reports whether a file changed: the platform's own,
// and beside it an sh launcher wherever that is another file, since Git Bash
// never runs a .cmd for a bare `aphrollo`. It never touches PATH or a shell's
// startup files: putting root on PATH is install's, through the user PATH it
// converges.
func WriteLauncher(root, fallback string) (bool, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false, err
	}
	changed, err := writeIfChanged(LauncherPath(root), launcherBody(filepath.ToSlash(root), fallback))
	if err != nil || launcherName == BinName {
		return changed, err
	}
	shChanged, err := writeIfChanged(filepath.Join(root, BinName), shLauncherBody(root, fallback))
	return changed || shChanged, err
}

// writeIfChanged writes want to path through a temp file and a rename, and
// leaves a file that already holds it alone.
func writeIfChanged(path, want string) (bool, error) {
	if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
		return false, nil
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(want), 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// shLauncherBody is the sh launcher: the user-space current, else fallback,
// else one line and exit 127.
func shLauncherBody(root, fallback string) string {
	return "#!/bin/sh\n" + Prelude(root, fallback) +
		"[ -x \"$x\" ] || { echo \"aphrollo: no binary found (run: aphrollo update)\" >&2; exit 127; }\n" +
		"exec \"$x\" \"$@\"\n"
}

// LegacyFallback is the installed path a launcher or hook falls back to when
// nothing names one.
func LegacyFallback() string { return legacyFallback }

// lookPathFn indirects exec.LookPath so a test can say what PATH resolves.
var lookPathFn = exec.LookPath

// PathCheck is one line when typing `aphrollo` in this process would not run
// the user-space install, and "" when it would or when no version is
// installed to compare with. A process keeps the PATH it started with, so
// this judges this shell; PathCheckIn judges the next one.
func PathCheck(root string) string {
	resolved, err := lookPathFn("aphrollo")
	return pathVerdict(root, resolved, err == nil)
}

// PathCheckIn is PathCheck for the PATH a new shell gets, dirs in order:
// what install converged, wherever the platform keeps it.
func PathCheckIn(root string, dirs []string) string {
	resolved, found := lookIn(dirs, "aphrollo")
	return pathVerdict(root, resolved, found)
}

func pathVerdict(root, resolved string, found bool) string {
	cur, haveCur := Current(root)
	if !haveCur {
		return ""
	}
	fix := "put " + root + " first on PATH"
	if !found {
		return "aphrollo is not on PATH; " + fix
	}
	if sameFile(resolved, LauncherPath(root)) || sameFile(resolved, BinaryPath(root, cur)) {
		return ""
	}
	return fmt.Sprintf("aphrollo on PATH resolves to %s, not the user-space install; %s", resolved, fix)
}

// lookIn is the file a shell runs for name with dirs as its PATH: the first
// dir holding one of the names the platform tries.
func lookIn(dirs []string, name string) (string, bool) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, n := range commandNames(name) {
			if p := filepath.Join(dir, n); isFile(p) {
				return p, true
			}
		}
	}
	return "", false
}

func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}
