package proc

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// A gate process that starts a copy of itself (the detached phase runner, the
// background sweep) must never start the wrong binary. When the running
// executable is a Go test binary, its argv is not a gate verb: the testing
// package stops flag parsing at the first non-flag word and runs the WHOLE
// suite, whose session-start tests start the copy again. One run fans out into
// a process per test, each of which does the same — the runaway that filled a
// Windows box with ~1,900 `tdd.test.exe` (#997).

// ErrTestBinary is what CheckSelfSpawn answers for a Go test binary.
var ErrTestBinary = errors.New("the running executable is a Go test binary, not a gate binary")

// ErrSpawnDepth is what CheckSelfSpawn answers past MaxSpawnDepth.
var ErrSpawnDepth = errors.New("self-spawn chain is already too deep")

// SpawnDepthEnv counts how many self-spawns deep this process is.
const SpawnDepthEnv = "APHROLLO_SPAWN_DEPTH"

// MaxSpawnDepth is how many generations of self-spawn are allowed: the hook
// spawns a phase runner, and the runner may spawn one more helper. A third
// generation is a loop.
const MaxSpawnDepth = 2

// IsGoTestBinary reports whether path names the binary `go test` builds: a
// `<pkg>.test` (`.test.exe` on Windows) file.
func IsGoTestBinary(path string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(path, `\`, "/")))
	return strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".test.exe")
}

// CheckSelfSpawn answers nil when exe may be started as a detached copy of
// this process: it is not a Go test binary, and env carries a spawn depth
// below MaxSpawnDepth.
func CheckSelfSpawn(exe string, env []string) error {
	if IsGoTestBinary(exe) {
		return fmt.Errorf("%w: %s", ErrTestBinary, exe)
	}
	if spawnDepth(env) >= MaxSpawnDepth {
		return ErrSpawnDepth
	}
	return nil
}

// ChildEnv returns base with the spawn depth one generation deeper than
// parent's: a child built from a scrubbed copy of the environment still counts
// the generation it is.
func ChildEnv(parent, base []string) []string {
	next := SpawnDepthEnv + "=" + strconv.Itoa(spawnDepth(parent)+1)
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if !strings.HasPrefix(kv, SpawnDepthEnv+"=") {
			out = append(out, kv)
		}
	}
	return append(out, next)
}

func spawnDepth(env []string) int {
	depth := 0
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, SpawnDepthEnv+"="); ok {
			if n, err := strconv.Atoi(v); err == nil {
				depth = n
			}
		}
	}
	return depth
}

// RefuseTestReexec is the test binary's own guard, called first thing in every
// TestMain. A test binary is only ever started with flags; a first argument
// that is not one means something started it as if it were the gate binary
// (`tdd.test gate runphase ...`), and running the suite would answer that with
// a fresh copy of itself. msg says why it refused.
func RefuseTestReexec(args []string) (msg string, refuse bool) {
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return "", false
	}
	return fmt.Sprintf("%s: refusing to run the test suite as %q — a test binary started as a gate binary re-runs itself without end", filepath.Base(args[0]), args[1]), true
}
