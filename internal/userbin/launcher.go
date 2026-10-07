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

// WriteLauncher writes the launcher under root, falling back to fallback the
// way the hooks do, and reports whether the file changed. It never touches
// PATH or a shell's startup files: putting root on PATH is the account's own.
func WriteLauncher(root, fallback string) (bool, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false, err
	}
	path := LauncherPath(root)
	want := launcherBody(filepath.ToSlash(root), fallback)
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

// LegacyFallback is the installed path a launcher or hook falls back to when
// nothing names one.
func LegacyFallback() string { return legacyFallback }

// lookPathFn indirects exec.LookPath so a test can say what PATH resolves.
var lookPathFn = exec.LookPath

// PathCheck is one line when typing `aphrollo` would not run the user-space
// install, and "" when it would or when no version is installed to compare with.
func PathCheck(root string) string {
	cur, haveCur := Current(root)
	if !haveCur {
		return ""
	}
	fix := "put " + root + " first on PATH"
	resolved, err := lookPathFn("aphrollo")
	if err != nil {
		return "aphrollo is not on PATH; " + fix
	}
	if sameFile(resolved, LauncherPath(root)) || (haveCur && sameFile(resolved, BinaryPath(root, cur))) {
		return ""
	}
	return fmt.Sprintf("aphrollo on PATH resolves to %s, not the user-space install; %s", resolved, fix)
}

func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}
