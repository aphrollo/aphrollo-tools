package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// realHomeAtStart is the operator's home as it stood before TestMain
// redirected it, so the isolation guard can name what must stay untouched.
// Empty when the box has no resolvable home.
var realHomeAtStart string

// stubDirs holds the temp dirs the compiled shim stubs are built into. They
// are built ONCE per test binary and shared by every test that needs them,
// so a t.Cleanup on the first test would pull the stub out from under the
// rest — the package's own run is the only lifetime that matches.
var (
	stubDirsMu sync.Mutex
	stubDirs   []string
)

func registerStubDir(dir string) {
	stubDirsMu.Lock()
	defer stubDirsMu.Unlock()
	stubDirs = append(stubDirs, dir)
}

// TestMain isolates the package's run from the operator's machine: the state
// dir, and every aphrollo lock file the shims take. Without the lock-dir
// override the shim tests wrote their target locks and slot files straight
// into the real %TEMP% and left them there.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aphrollo-cli-pkgtest-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude")); err != nil {
		panic(err)
	}
	redirectHome(dir)
	locks := filepath.Join(dir, "locks")
	if err := os.MkdirAll(locks, 0o755); err != nil {
		panic(err)
	}
	// The gate may run this suite from inside a deferred phase, which tells
	// its child the build lock is HELD. That is true of the phase, not of a
	// test case about the shim's queuing — inherited, it made every shim
	// test take the nested-passthrough path.
	os.Unsetenv(tdd.BuildLockHeldEnv)
	// And the same net for gh: this package's verbs shell out to it, and only
	// the tests that arranged a stub were isolated from the operator's real
	// one. See ghstub_isolation_test.go.
	if stub, err := ghStubDir(); err == nil {
		if err := os.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			panic(err)
		}
	}
	restoreLocks := tdd.SetLockDirForTest(locks)
	// The real smoke check spawns the candidate binary (see selfinstall.go);
	// nothing in this package's suite ever builds one — buildAphrollo is
	// stubbed everywhere it is reached — so it would refuse every fixture's
	// plain-byte "binary". Default it permissive here; the one test that
	// pins the refusal overrides it locally.
	runSmokeCheckFn = func(string) error { return nil }
	code := m.Run()
	restoreLocks()
	os.RemoveAll(dir)
	stubDirsMu.Lock()
	for _, d := range stubDirs {
		os.RemoveAll(d)
	}
	stubDirsMu.Unlock()
	os.Exit(code)
}

// redirectHome points every home-derived default at a temp home for the
// package's whole run. Two of this package's defaults come from the home dir
// rather than from an override -- the Claude config dir when
// CLAUDE_CONFIG_DIR is empty, and the global git hooks dir -- and a test that
// takes either one writes into the operator's live install. A `gate init`
// with a defaulted hooks dir rewrote this box's real pre-commit hook to point
// at a temp binary, and nothing noticed until the next commit.
//
// The Go cache variables are pinned to their resolved values FIRST: they
// default under the home dir, and moving them would make every `go` a test
// spawns rebuild the world into an empty cache.
func redirectHome(dir string) {
	if home, err := os.UserHomeDir(); err == nil {
		realHomeAtStart = home
	}
	pinGoEnv()
	fake := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fake, ".config"), 0o755); err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(k, fake); err != nil {
			panic(err)
		}
	}
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(fake, ".config")); err != nil {
		panic(err)
	}
}

// pinGoEnv writes the toolchain's resolved cache locations into the
// environment, so redirecting HOME cannot move them.
func pinGoEnv() {
	names := []string{"GOPATH", "GOCACHE", "GOMODCACHE"}
	out, err := exec.Command("go", append([]string{"env"}, names...)...).Output()
	if err != nil {
		return
	}
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n"), "\n")
	for i, name := range names {
		if i >= len(lines) {
			break
		}
		if v := strings.TrimSpace(lines[i]); v != "" {
			_ = os.Setenv(name, v)
		}
	}
}
