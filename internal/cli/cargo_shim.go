package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// exCargoTempFail is sysexits.h's EX_TEMPFAIL: "temporary failure, indicating
// something that is not really an error" -- exactly what a give-up-waiting
// outcome is (the build lock is fine, this invocation just couldn't get it
// in time), distinct from a real cargo failure (whatever cargo itself
// exits with, propagated unchanged) or a usage error.
const exCargoTempFail = 75

// defaultCargoWaitBudget bounds how long `aphrollo tdd cargo` waits for the
// machine-wide build lock before giving up. 20 minutes: a direct cargo
// invocation is a deliberate, interactive action (unlike a hook's fast
// between-edit run), so it is worth waiting a long time for rather than
// failing fast -- but not forever, since an indefinite wait with zero
// feedback is the exact problem this shim exists to fix (a session sitting
// 15 minutes with no visibility, before this existed).
const defaultCargoWaitBudget = 20 * time.Minute

// defaultCargoPollInterval is how often the shim's wait loop rechecks the
// lock once it knows it must wait.
const defaultCargoPollInterval = time.Second

// cargoShimConfig bundles the shim's tunable knobs so tests can shrink the
// wait budget/poll interval without touching production defaults (mirrors
// buildlock.go's buildLockPostEditDeadline/buildLockPrecommitDeadline
// pattern: production code always uses the real values; only a test
// constructs a cargoShimConfig with shorter ones).
type cargoShimConfig struct {
	waitBudget   time.Duration
	pollInterval time.Duration
	realCargo    string // resolved path to the ACTUAL cargo binary
}

// runTDDCargo is the `aphrollo tdd cargo [cargo args...]` entry point: it
// resolves the real cargo binary and the configured wait budget from the
// environment, then delegates to runCargoShim (the testable core).
func runTDDCargo(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	realCargo, err := resolveRealCargo()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo tdd cargo: %v\n", err)
		return 1
	}
	cfg := cargoShimConfig{
		waitBudget:   defaultCargoWaitBudget,
		pollInterval: defaultCargoPollInterval,
		realCargo:    realCargo,
	}
	if raw := strings.TrimSpace(os.Getenv("APHROLLO_CARGO_WAIT_SECS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			cfg.waitBudget = time.Duration(n) * time.Second
		}
	}
	return runCargoShim(args, stdin, stdout, stderr, cfg)
}

// runCargoShim is the shim's testable core: no lock (a hooks/gate cargo run
// already holds it -- APHROLLO_BUILD_LOCK_HELD=1) passes straight through;
// an uncontended lock is silent (acquire, run, done); a contended lock
// prints exactly ONE "queued behind" line the moment contention is
// discovered, polls silently, and on success prints exactly ONE "lock
// acquired after" line -- never a repeated/periodic line. Giving up after
// waitBudget prints the give-up line and returns exCargoTempFail.
func runCargoShim(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg cargoShimConfig) int {
	if os.Getenv(tdd.BuildLockHeldEnv) == "1" {
		// The hooks/gates already hold the SAME machine-wide lock (this
		// process is a nested cargo invocation resolved through the shim
		// because the shim dir precedes the real cargo on PATH) -- touching
		// the lock again here would deadlock against ourselves.
		return execCargo(cfg.realCargo, args, stdin, stdout, stderr)
	}

	release, ok := tdd.TryAcquireBuildLock()
	if ok {
		return runWithLock(release, cfg.realCargo, args, stdin, stdout, stderr)
	}

	// Contended: report it exactly once, then poll silently.
	fmt.Fprintln(stderr, queuedLine())
	start := time.Now()
	for {
		elapsed := time.Since(start)
		if elapsed >= cfg.waitBudget {
			fmt.Fprintln(stderr, giveUpLine(elapsed))
			return exCargoTempFail
		}
		time.Sleep(cfg.pollInterval)
		if release, ok := tdd.TryAcquireBuildLock(); ok {
			fmt.Fprintln(stderr, acquiredLine(time.Since(start)))
			return runWithLock(release, cfg.realCargo, args, stdin, stdout, stderr)
		}
	}
}

// runWithLock writes the owner file, runs the real cargo, then removes the
// owner file and releases the lock -- in that order, so the lock is held
// for the SHORTEST time that still covers the owner file's validity window.
func runWithLock(release func(), realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	defer release()
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown cwd)"
	}
	tdd.WriteBuildLockOwner("cargo "+strings.Join(args, " "), cwd)
	defer tdd.RemoveBuildLockOwner()
	return execCargo(realCargo, args, stdin, stdout, stderr)
}

// resolveRealCargo resolves the ACTUAL cargo binary the shim must run --
// never via a bare PATH lookup, which would find the shim itself (it is
// meant to sit earlier on PATH than the real cargo). APHROLLO_REAL_CARGO
// overrides everything (tests set this so they never need a real cargo
// install); otherwise it is CARGO_HOME's bin dir (or ~/.cargo/bin when
// CARGO_HOME is unset), which is where rustup installs its own cargo PROXY
// binary -- "the real cargo" from the shim's point of view, whichever
// toolchain it then dispatches to.
func resolveRealCargo() (string, error) {
	if override := os.Getenv("APHROLLO_REAL_CARGO"); override != "" {
		return override, nil
	}
	home := os.Getenv("CARGO_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve cargo: no CARGO_HOME set and no home dir: %w", err)
		}
		home = filepath.Join(userHome, ".cargo")
	}
	exeName := "cargo"
	if runtime.GOOS == "windows" {
		exeName = "cargo.exe"
	}
	path := filepath.Join(home, "bin", exeName)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("resolve cargo: %s not found (CARGO_HOME=%q): %w", path, home, err)
	}
	return path, nil
}

// execCargo runs the real cargo binary with args, inheriting stdin/stdout/
// stderr (a *os.File writer/reader is passed straight through to the child
// process by the os/exec package; a non-file io.Reader/Writer, as tests use,
// is piped) and the current process's own environment/cwd, propagating its
// exit code. APHROLLO_BUILD_LOCK_HELD=1 rides along in the child's
// environment too, so a build script that itself shells out to `cargo`
// (resolved through the shim again, if PATH still has it first) passes
// straight through instead of deadlocking.
func execCargo(realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := exec.Command(realCargo, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), tdd.BuildLockHeldEnv+"=1")
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	fmt.Fprintf(stderr, "aphrollo tdd cargo: %v\n", err)
	return 1
}

// queuedLine composes the ONE-SHOT "queued behind" message printed the
// moment the shim first discovers the lock is contended (never repeated) --
// naming the holder from the owner file when it's readable.
func queuedLine() string {
	o, ok := tdd.ReadBuildLockOwner()
	if !ok {
		return "cargo: queued behind another build (holder unknown) -- waiting for the machine-wide build lock"
	}
	return fmt.Sprintf("cargo: queued behind %q in %s (pid %d, held %s)", o.Cmd, o.Cwd, o.PID, formatMinSec(time.Since(o.Started)))
}

// acquiredLine composes the ONE-SHOT message printed once the shim acquires
// the lock AFTER having had to wait for it (never printed for an
// uncontended acquire, which stays silent).
func acquiredLine(waited time.Duration) string {
	return fmt.Sprintf("cargo: lock acquired after %ds", int(waited.Seconds()+0.5))
}

// giveUpLine composes the message printed when the shim stops waiting
// without ever acquiring the lock.
func giveUpLine(elapsed time.Duration) string {
	holder := "(unknown)"
	if o, ok := tdd.ReadBuildLockOwner(); ok {
		holder = fmt.Sprintf("%q in %s (pid %d)", o.Cmd, o.Cwd, o.PID)
	}
	return fmt.Sprintf("cargo: gave up after %ds waiting for the build lock (holder: %s)", int(elapsed.Seconds()+0.5), holder)
}

// formatMinSec renders a duration as "<M>m <S>s" for the queued line's
// "held" clause -- a build lock can plausibly be held for many minutes, so
// a bare seconds count would be harder to read at a glance.
func formatMinSec(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) - m*60
	return fmt.Sprintf("%dm %ds", m, s)
}
