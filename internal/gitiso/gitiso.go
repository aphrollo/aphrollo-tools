// Package gitiso is the one place a test binary is cut off from the git state
// of the box it runs on: the repositories around it, the environment a git hook
// or a gate spawn handed it, and the operator's own git config. Every
// package's TestMain calls Main (or Isolate, when it has setup of its own), so
// a fixture that runs a bare `git init`, `git config` or `git commit` can only
// ever reach a repository the test made.
package gitiso

import (
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
)

// Main isolates the calling package's run, runs it, removes what the isolation
// made and returns the exit code for os.Exit. It is the whole TestMain of a
// package with no setup of its own: the package passes a closure over m.Run,
// which keeps the call in TestMain's own text, where the test_main_exit law
// looks for it.
func Main(run func() int) int {
	root, err := MkRoot("aphrollo-gi-")
	if err != nil {
		panic(err)
	}
	if _, err := Isolate(root); err != nil {
		panic(err)
	}
	code := run()
	RemoveAll(root)
	return code
}

// staleRoot is how old a root directory of a finished-or-killed run must be
// before the next run of its family removes it. No test binary of this module
// runs for anything near it.
const staleRoot = 2 * time.Hour

// MkRoot makes the root directory of a test binary's run under the temp dir,
// named with prefix. A binary killed by a timeout or a signal never reaches
// the removal at the end of its TestMain, so before making its own root it
// removes the directories of the same family older than staleRoot: the temp
// dir, RAM-backed on some boxes, does not fill up one killed run at a time.
func MkRoot(prefix string) (string, error) {
	tmp := os.TempDir()
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Before(time.Now().Add(-staleRoot)) {
				RemoveAll(filepath.Join(tmp, e.Name()))
			}
		}
	}
	return os.MkdirTemp(tmp, prefix)
}

// RemoveAll removes dir and everything under it, making read-only
// directories writable first: a Go module cache is read-only, and so is a
// directory a test took away its own write bit on, and os.RemoveAll leaves
// either behind.
func RemoveAll(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

// homeLayout maps every variable a Go program, git, or a tool a test spawns
// resolves the operator's home, config, data or cache directory from to its
// directory under fake.
func homeLayout(fake string) map[string]string {
	return map[string]string{
		"HOME":            fake,
		"USERPROFILE":     fake,
		"APPDATA":         filepath.Join(fake, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(fake, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(fake, ".config"),
		"XDG_DATA_HOME":   filepath.Join(fake, ".local", "share"),
		"XDG_CACHE_HOME":  filepath.Join(fake, ".local", "cache"),
		"XDG_STATE_HOME":  filepath.Join(fake, ".local", "state"),
	}
}

// Isolate cuts the rest of this process's life off from the box's git world
// and answers the fake home it made under root, which the caller owns and
// removes. It
//   - drops every GIT_* variable the process inherited, so a hook's GIT_DIR,
//     GIT_INDEX_FILE or GIT_WORK_TREE can not redirect a fixture's git at the
//     repository the hook runs for;
//   - moves TMPDIR, TMP, TEMP and GOTMPDIR (which t.TempDir reads first) under root, and sets GIT_CEILING_DIRECTORIES
//     to root and to the root of the repository the process was started in,
//     so no directory a test makes, and no package directory it runs from, can
//     discover a real repository by walking up;
//   - points HOME and every spelling of it, GIT_CONFIG_GLOBAL and the XDG
//     directories into the fake home, and switches the system config off, so
//     `git config --global` writes a file the test owns, one that already
//     tells git to take Windows paths past 260 characters, which nested temp
//     roots reach;
//   - sends CLAUDE_CONFIG_DIR, where the gate keeps its state, under root, and
//     points core.hooksPath at an empty dir so no repo hook a test reaches runs;
//   - switches git's post-commit auto maintenance off.
//   - sets GOWORK=off, so no go.work of the box, the runner or a directory above
//     the temp root is in view of a `go` a test spawns in a throwaway module.
//
// The Go toolchain's cache locations are pinned where they resolve now, so
// moving the home does not make every `go` a test spawns rebuild the world.
func Isolate(root string) (home string, err error) {
	pinToolchainHomes()
	tmp := filepath.Join(root, "tmp")
	home = filepath.Join(root, "home")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	env := map[string]string{"TMPDIR": tmp, "TMP": tmp, "TEMP": tmp, "GOTMPDIR": tmp, "GOWORK": "off"}
	for name, path := range homeLayout(home) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", err
		}
		env[name] = path
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[core]\n\tlongpaths = true\n"), 0o644); err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	env["GIT_CEILING_DIRECTORIES"] = gitenv.CeilingList(root, enclosingRepo(cwd))
	env["GIT_CONFIG_GLOBAL"] = gitconfig
	env["GIT_CONFIG_NOSYSTEM"] = "1"
	// The gate's state dir is found through CLAUDE_CONFIG_DIR before the home.
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(root, "claude")
	// The environment changes only once everything it names exists: a root that
	// cannot hold them leaves the process as it was.
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GIT_") {
			_ = os.Unsetenv(name) // a name taken from the environment is always valid
		}
	}
	for name, value := range env {
		_ = os.Setenv(name, value) // fails only on an empty or malformed name, and these are literals
	}
	gitenv.DisableMaintenanceAndHooks(filepath.Join(root, "nohooks"), func(k, v string) { _ = os.Setenv(k, v) })
	return home, nil
}

// MustIsolate is Isolate for a TestMain that has setup of its own: it panics
// when the isolation cannot be made, since no test may run unisolated.
func MustIsolate(root string) (home string) {
	home, err := Isolate(root)
	if err != nil {
		panic(err)
	}
	return home
}

// enclosingRepo is the nearest directory at or above dir holding a `.git`,
// file or directory, or "" when there is none: the checkout a test binary is
// started inside, which git would otherwise find from the package directory.
func enclosingRepo(dir string) string {
	// walk-terminates: dir becomes its parent each turn, and the walk returns at the root
	for dir != "" {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// pinToolchainHomes writes the Go toolchain's resolved cache and env-file
// locations, and the rustup home, into the environment where they default
// under the home dir being replaced. A toolchain that cannot answer leaves the
// variables as they were.
func pinToolchainHomes() {
	// exec-ok: gitiso cannot import internal/run: run's own tests run under gitiso.Main, so the import would be a cycle in test.
	out, _ := exec.Command("go", "env", "-json", "GOPATH", "GOCACHE", "GOMODCACHE", "GOENV").Output() // stderr-ok: a failed lookup leaves the variables unpinned, and go says nothing a caller could use
	var resolved map[string]string
	_ = json.Unmarshal(out, &resolved)
	for name, value := range resolved {
		_ = os.Setenv(name, value)
	}
	home, _ := os.UserHomeDir()
	_ = os.Setenv("RUSTUP_HOME", cmp.Or(os.Getenv("RUSTUP_HOME"), filepath.Join(home, ".rustup")))
}
