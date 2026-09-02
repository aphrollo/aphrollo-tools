package tdd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// The gate runs git constantly, and a session puts the queue shim dir FIRST
// on PATH — so a bare `git` resolves to the shim's git.cmd, whose cmd.exe
// wrapper rewrites arguments on the way through (a caret is an escape
// character there). That is not a slow path, it is a wrong answer: `rev-parse
// MERGE_HEAD^{tree}` arrived as `HEAD{tree}`, mergeTipTree returned "", and
// every merge was refused for having no lane tip. So the gate resolves the
// real git itself, and no git argument in this package uses caret syntax.

// realGitEnv lets an operator (and the shim's own tests) name the git binary
// outright, matching the shim's override.
const realGitEnv = "APHROLLO_REAL_GIT"

// gitBinaryCache holds the resolution for one PATH value. Keying on PATH
// rather than a sync.Once keeps it correct when PATH changes under us, which
// is exactly what a test that plants a shim does.
var gitBinaryCache struct {
	sync.Mutex
	path, forPATH string
}

// gitBinary is the git this package execs: the first PATH entry holding a git
// that is not a shim. It falls back to the bare name, so a box this resolver
// cannot make sense of behaves exactly as before.
func gitBinary() string {
	if override := os.Getenv(realGitEnv); override != "" {
		return override
	}
	env := os.Getenv("PATH")
	gitBinaryCache.Lock()
	defer gitBinaryCache.Unlock()
	if gitBinaryCache.path != "" && gitBinaryCache.forPATH == env {
		return gitBinaryCache.path
	}
	resolved := "git"
	for _, dir := range filepath.SplitList(env) {
		if c := gitInDir(dir); c != "" {
			resolved = c
			break
		}
	}
	gitBinaryCache.path, gitBinaryCache.forPATH = resolved, env
	return resolved
}

// gitNames are the file names a `git` lookup would try in one directory, in
// the order the OS would try them.
func gitNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"git.exe", "git.com", "git.cmd", "git.bat", "git"}
	}
	return []string{"git"}
}

// gitInDir returns the git executable in dir, or "" when dir holds none — or
// holds a SHIM, in which case the whole directory is skipped rather than
// searched further: a shim dir is where a lookup goes wrong, not a fallback.
func gitInDir(dir string) string {
	if dir == "" {
		return ""
	}
	for _, name := range gitNames() {
		c := filepath.Join(dir, name)
		fi, err := os.Stat(c)
		if err != nil || fi.IsDir() {
			continue
		}
		if isGitShim(c) {
			return ""
		}
		return c
	}
	return ""
}

// isGitShim reports whether a candidate is a script that re-enters aphrollo.
// Only scripts are read: a compiled git is never one, and reading every
// binary on PATH to find out would be the expensive way to learn nothing.
func isGitShim(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".com", ".dll":
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(data) > 8192 {
		data = data[:8192]
	}
	return strings.Contains(strings.ToLower(string(data)), "aphrollo")
}
