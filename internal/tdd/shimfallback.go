package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// realToolPath resolves the tool a shim shadows, skipping the shim directory
// itself and any other copy of the shim.
//
// It has to be answered at INSTALL time. The shim's own directory is ahead of
// the real tool's on PATH — that is what makes the shim a shim — so a lookup
// from inside the script finds the script. Returning "" is a real answer: the
// tool genuinely is not on PATH, and the shim then reports the missing binary
// rather than exec-ing nothing.
func realToolPath(sub, shimDir string) string {
	shim := filepath.Clean(shimDir)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || filepath.Clean(dir) == shim {
			continue
		}
		for _, name := range toolNames(sub) {
			candidate := filepath.Join(dir, name)
			if isExecutableFile(candidate) {
				return candidate
			}
		}
	}
	// A last resort that still skips the shim: LookPath honours PATHEXT on
	// Windows, so it finds forms toolNames does not enumerate.
	if p, err := exec.LookPath(sub); err == nil && filepath.Clean(filepath.Dir(p)) != shim {
		return p
	}
	return ""
}

// toolNames are the file names one tool can have on this platform. The
// extensionless form is the POSIX one and also what Git Bash's own git is
// called; the .exe form is what Windows resolves.
func toolNames(sub string) []string {
	return []string{sub + ".exe", sub}
}

// isExecutableFile reports whether path is a regular file this box would run.
// The executable BIT is not consulted: Windows has none, and on POSIX a shim
// directory entry that is not executable is not what a PATH lookup would pick
// anyway — the caller only reaches here for candidates it named itself.
func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	if strings.HasSuffix(strings.ToLower(path), ".exe") {
		return true
	}
	return fi.Mode()&0o111 != 0 || os.Getenv("OS") != ""
}
