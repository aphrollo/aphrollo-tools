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

// exCargoUsage is what the shim returns when it REFUSES an invocation
// outright: sysexits.h's EX_USAGE, distinct from a cargo failure and from a
// give-up-waiting outcome.
const exCargoUsage = 64

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

// runGateCargo is the `aphrollo tdd cargo [cargo args...]` entry point: it
// resolves the real cargo binary and the configured wait budget from the
// environment, then delegates to runCargoShim (the testable core).
func runGateCargo(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
		// The hooks/gates already hold a slot for the SAME target dir (this
		// process is a nested cargo invocation resolved through the shim
		// because the shim dir precedes the real cargo on PATH) -- taking
		// another slot here would deadlock against ourselves.
		return execCargoHeld(cfg.realCargo, args, stdin, stdout, stderr, 0, true)
	}

	target := shimTargetDir()
	if refuseBareMutants(args, target, stderr) {
		// Not a cargo failure and not a usage error of cargo's: the
		// invocation is the wrong ENTRY POINT, and the line above names the
		// right one.
		return exCargoUsage
	}

	if isCargoReadOnlyVerb(args) {
		// Reads the manifest/lockfile and compiles nothing, so it is not
		// what the slots govern -- and it is exactly what a tool-detection
		// path (`cargo --version`, `cargo metadata`) calls, which must
		// never sit behind a multi-minute build.
		return execCargo(cfg.realCargo, args, stdin, stdout, stderr, 0)
	}

	if queueBypassAllowed(target) {
		// The mutation job owns this target dir outright, so there is nothing
		// for it to contend with. It is told the lock is NOT held, because it
		// holds none: a child that queues for some other directory it happens
		// to build in must still queue for that one.
		logQueueBypass(target)
		return execCargo(cfg.realCargo, args, stdin, stdout, stderr, 0)
	}
	acquire := acquireFor(args)
	slot, releaseTarget, releaseAll, ok := acquire(target, shimOwnerCommand(args), shimCwd())
	if ok {
		return runWithLock(slot, releaseTarget, releaseAll, cfg.realCargo, args, stdin, stdout, stderr)
	}

	// Contended: report it exactly once, then poll silently.
	fmt.Fprintln(stderr, queuedLine(target))
	start := time.Now()
	for {
		elapsed := time.Since(start)
		if elapsed >= cfg.waitBudget {
			fmt.Fprintln(stderr, giveUpLine(elapsed, target))
			return exCargoTempFail
		}
		time.Sleep(cfg.pollInterval)
		if slot, releaseTarget, releaseAll, ok := acquire(target, shimOwnerCommand(args), shimCwd()); ok {
			fmt.Fprintln(stderr, acquiredLine(time.Since(start)))
			return runWithLock(slot, releaseTarget, releaseAll, cfg.realCargo, args, stdin, stdout, stderr)
		}
	}
}

// acquireFor picks the acquisition shape this invocation needs. A long verb
// needs its two releases separately (the target for the prewarm, the global
// slot for the whole run); everything else releases both together.
func acquireFor(args []string) func(target, cmd, cwd string) (tdd.BuildSlot, func(), func(), bool) {
	if isCargoLongVerb(args) {
		return tdd.TryAcquireLongVerbSlot
	}
	return func(target, cmd, cwd string) (tdd.BuildSlot, func(), func(), bool) {
		slot, release, ok := tdd.TryAcquireBuildSlot(target, cmd, cwd)
		return slot, release, release, ok
	}
}

// shimOwnerCommand and shimCwd are what a waiting session reads about this
// invocation. They are computed BEFORE the acquisition, because the record is
// written by the acquisition itself — there is no later moment at which a
// caller could forget to write it.
func shimOwnerCommand(args []string) string {
	return "cargo " + strings.Join(args, " ")
}

func shimCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "(unknown cwd)"
	}
	return cwd
}

// shimTargetDir resolves the target dir THIS invocation's build writes to,
// from the shim's own cwd -- the same resolution the hooks/gates use, so a
// direct `cargo build` and a gate's build contend exactly when they share a
// build directory and never when they don't.
func shimTargetDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return tdd.ResolveCargoTargetDir(cwd)
}

// runWithLock writes the owner file, runs the real cargo, then removes the
// owner file and releases the lock -- in that order, so the lock is held
// for the SHORTEST time that still covers the owner file's validity window.
// `cargo run` is a special case (task A9): its own launched process's
// lifetime must NEVER be covered by the lock, only the build that precedes
// it, so that path is split out entirely.
func runWithLock(slot tdd.BuildSlot, releaseTarget, releaseAll func(), realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if isCargoRunVerb(args) {
		return runCargoRunSplitLock(slot, releaseAll, realCargo, args, stdin, stdout, stderr)
	}
	if isCargoLongVerb(args) {
		return runCargoLongVerbSplitLock(slot, releaseTarget, releaseAll, realCargo, args, stdin, stdout, stderr)
	}
	defer releaseAll()
	return execCargo(realCargo, args, stdin, stdout, stderr, slot.Jobs)
}

// runCargoRunSplitLock implements task A9's fix: `cargo run`'s machine-wide
// lock must cover only the BUILD, never the launched process's entire
// lifetime -- a live `cargo run -p server` used to hold the lock forever,
// blocking every other cargo on the box ("queued behind cargo run -p
// server", against a SERVER that never exits). Under the lock: build the
// same target `cargo run` would (the verb swapped run -> build, everything
// from the first bare "--" onward dropped -- those are the launched
// program's own arguments, meaningless to `cargo build`). A failing
// build's exit code propagates immediately and `run` never executes. On a
// successful build the lock is released BEFORE running -- the build is
// fresh, so cargo's own `run` immediately execs the already-built binary
// rather than rebuilding -- and the ORIGINAL args run lock-free, inheriting
// stdio for the launched process's full lifetime. `nextest run`/`test`
// deliberately do NOT take this path (isCargoRunVerb only matches the bare
// `run` verb, not nextest's own `run` sub-subcommand): their own execution
// IS the thing this lock exists to serialize.
func runCargoRunSplitLock(slot tdd.BuildSlot, release func(), realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	buildArgs := cargoRunArgsToBuildArgs(args)
	buildCode := execCargo(realCargo, buildArgs, stdin, stdout, stderr, slot.Jobs)
	release()
	if buildCode != 0 {
		return buildCode
	}
	return execCargo(realCargo, args, stdin, stdout, stderr, 0)
}

// cargoLongVerbs run for a very long time WITHOUT compiling into the
// caller's target dir for most of it: `mutants` copies the tree to its own
// directory before mutating (it held the lock for entire multi-hour runs),
// `bench` spends its time executing, `install` builds in its own temp dir.
// So the slot covers a prewarm compile and nothing else. `watch` is NOT one
// of them: it recompiles on every save for as long as it is open, so
// "prewarm once, then unlocked forever" would hand the box to a process
// that never stops building.
// `nextest run`/`test` are absent on purpose -- their execution IS what the
// slots govern.
var cargoLongVerbs = map[string]bool{
	"mutants": true,
	"bench":   true,
	"install": true,
}

func isCargoLongVerb(args []string) bool {
	return cargoLongVerbs[cargoVerb(args)]
}

// cargoPrewarmArgs is the compile a long verb holds its slot for: mutants
// only needs the tree to typecheck before it forks off its own copies;
// everything else wants the test binaries built. It is deliberately
// unscoped -- inferring `-p` selection from an arbitrary long verb's own
// flag grammar would be guesswork, and a whole-workspace warm-up is what
// the long run is about to need anyway.
func cargoPrewarmArgs(args []string) []string {
	switch cargoVerb(args) {
	case "mutants":
		return []string{"check", "--tests"}
	case "bench":
		// A bench run compiles BENCH targets; warming --tests warmed the
		// wrong thing and left the real compile to run unslotted.
		return []string{"build", "--benches"}
	default:
		return []string{"build", "--tests"}
	}
}

// runCargoLongVerbSplitLock holds the slot for a prewarm compile only, then
// releases it and runs the long verb unlocked. The prewarm is a warm-up,
// not a gate: its exit code is discarded (unlike `cargo run`'s build, which
// IS the thing being launched), so the operator always gets the exit code of
// the command they typed. Outside a cargo project there is nothing to warm,
// and the slot is released without compiling anything.
func runCargoLongVerbSplitLock(slot tdd.BuildSlot, releaseTarget, releaseAll func(), realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	defer releaseAll()
	if cwd, err := os.Getwd(); err == nil && insideCargoProject(cwd) {
		execCargo(realCargo, cargoPrewarmArgs(args), stdin, stdout, stderr, slot.Jobs)
	}
	// The caller's target is free from here: the long phase builds in copies
	// of the tree (mutants) or not at all. The GLOBAL slot stays held for the
	// whole run and is lent to the children, so a four-job mutation run costs
	// one slot rather than every slot on the box.
	releaseTarget()
	return execCargoToken(realCargo, args, stdin, stdout, stderr, 0, tdd.SlotTokenEnvEntry(slot))
}

// insideCargoProject reports whether dir or an ancestor holds a Cargo.toml.
func insideCargoProject(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// cargoVerb returns cargo's subcommand -- the first argv entry that does
// not start with "-" -- or "" if args is all options. Cargo's own global
// options (-v, --color=always, ...) may precede the subcommand; this does
// not attempt to skip a SEPARATE-argument option value (e.g. "--color
// always" splits across two argv entries), which is good enough to find
// the verb without implementing cargo's full CLI grammar -- the hooks/gates
// and a session's own direct invocations never put a value-taking global
// flag ahead of the subcommand.
func cargoVerb(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// isCargoRunVerb reports whether args invoke `cargo run` (task A9's
// split-lock path) as opposed to any other subcommand -- crucially
// EXCLUDING `nextest run` (its verb is "nextest", not "run") and `test`:
// their own execution IS what the lock exists to serialize, so they
// deliberately keep the lock for the whole call.
func isCargoRunVerb(args []string) bool {
	return cargoVerb(args) == "run"
}

// cargoReadOnlyVerbs compile nothing into the target dir: they read the
// manifest, the lockfile or the source text. `check` and `clippy` are
// deliberately absent -- they run the compiler front end into the SHARED
// target dir, which is precisely what the slots govern.
var cargoReadOnlyVerbs = map[string]bool{
	"metadata":       true,
	"tree":           true,
	"fmt":            true,
	"locate-project": true,
	"pkgid":          true,
	"read-manifest":  true,
}

// isCargoReadOnlyVerb reports whether args bypass the build slots entirely.
// `--version`/`-V` have no verb at all, so they are matched as flags -- but
// only BEFORE the first bare "--", since past that they belong to the
// launched program (`cargo test -- --version` compiles a test binary).
func isCargoReadOnlyVerb(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--version" || a == "-V" {
			return true
		}
	}
	return cargoReadOnlyVerbs[cargoVerb(args)]
}

// cargoRunArgsToBuildArgs converts `cargo run`'s argv into the equivalent
// `cargo build` argv: the verb becomes "build", and everything from the
// first bare "--" onward is dropped (the launched program's own arguments,
// meaningless to `cargo build`).
func cargoRunArgsToBuildArgs(args []string) []string {
	out := make([]string, 0, len(args))
	verbReplaced := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if !verbReplaced && !strings.HasPrefix(a, "-") {
			out = append(out, "build")
			verbReplaced = true
			continue
		}
		out = append(out, a)
	}
	return out
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
// execCargoHookForTest, when set, is called with the resolved argv
// immediately before execCargo spawns the real cargo process -- test-only
// instrumentation (task A9) letting a test observe/assert the machine-wide
// lock's state (via tdd.TryAcquireBuildLock) at the exact moment each phase
// (build vs run) actually executes, without the external stub process
// needing to talk back to the Go test itself. Always nil in production.
var execCargoHookForTest func(args []string)

func execCargo(realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer, jobs int) int {
	return execCargoHeld(realCargo, args, stdin, stdout, stderr, jobs, jobs > 0)
}

// cargoChildEnv is the environment a launched cargo inherits. The
// build-lock-held marker is a FACT about this process, not a decoration: a
// child told the lock is held skips the queue, so claiming it while holding
// nothing (the read-only bypass, the post-release `cargo run` that outlives
// the slot) lets an unslotted build walk straight past every waiter.
func cargoChildEnv(held bool) []string {
	return cargoChildEnvToken(held, "")
}

// cargoChildEnvToken is cargoChildEnv plus an optional slot token: a long
// verb lends its ONE global slot to the cargo invocations it spawns, which is
// what stops a four-job `cargo mutants` run from taking every slot on the box.
// A child carrying the token still queues for the target lock of whatever
// directory it builds in, so it is never told the lock is HELD. With no token
// given, any inherited one is stripped — a launched `cargo run` process
// outlives the slot entirely.
func cargoChildEnvToken(held bool, token string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if ok && (k == tdd.BuildLockHeldEnv || k == tdd.SlotTokenEnvName()) {
			continue
		}
		out = append(out, kv)
	}
	if held {
		out = append(out, tdd.BuildLockHeldEnv+"=1")
	}
	if token != "" {
		out = append(out, token)
	}
	return out
}

func execCargoHeld(realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer, jobs int, held bool) int {
	return execCargoEnv(realCargo, args, stdin, stdout, stderr, jobs, cargoChildEnvToken(held, ""))
}

// execCargoToken runs cargo with its parent's slot token in the environment.
func execCargoToken(realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer, jobs int, token string) int {
	return execCargoEnv(realCargo, args, stdin, stdout, stderr, jobs, cargoChildEnvToken(false, token))
}

func execCargoEnv(realCargo string, args []string, stdin io.Reader, stdout, stderr io.Writer, jobs int, env []string) int {
	if execCargoHookForTest != nil {
		execCargoHookForTest(args)
	}
	cmd := exec.Command(realCargo, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env
	if jobs > 0 {
		// A slot is 1/N of the box: cap the child so N concurrent builds
		// into one target dir cost about what one uncapped build did. The
		// STRICTER of the slot's cap and a caller's own value wins.
		cmd.Env = tdd.EnvWithBuildJobs(cmd.Env, jobs)
	}
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
func queuedLine(targetDir string) string {
	o, ok := tdd.ReadBuildSlotOwner(targetDir)
	if !ok {
		return "cargo: queued behind another build (holder unknown) -- waiting for a build slot for " + targetDir
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
func giveUpLine(elapsed time.Duration, targetDir string) string {
	holder := "(unknown)"
	if o, ok := tdd.ReadBuildSlotOwner(targetDir); ok {
		holder = fmt.Sprintf("%q in %s (pid %d)", o.Cmd, o.Cwd, o.PID)
	}
	return fmt.Sprintf("cargo: gave up after %ds waiting for a build slot (holder: %s)", int(elapsed.Seconds()+0.5), holder)
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
