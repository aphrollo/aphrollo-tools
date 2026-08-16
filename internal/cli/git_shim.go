package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// exGitTempFail reuses sysexits.h's EX_TEMPFAIL (same value, same reasoning
// as cargo_shim.go's exCargoTempFail): the per-repo git lock is fine, this
// invocation just couldn't get it in time.
const exGitTempFail = 75

// defaultGitWaitBudget bounds how long `aphrollo tdd git` waits for the
// per-repo git lock before giving up. Same 20-minute reasoning as cargo's
// shim: a direct git invocation is deliberate and worth a long wait, but not
// an unbounded one.
const defaultGitWaitBudget = 20 * time.Minute

// defaultGitPollInterval mirrors cargo's -- how often the wait loop
// rechecks once it knows it must wait.
const defaultGitPollInterval = 250 * time.Millisecond

// gitLockFileName / gitOwnerFileName are the two files the shim creates
// inside a repo's COMMON git dir (task A11 requirement 3): the advisory OS
// lock itself, and the JSON owner record naming whoever holds it.
const (
	gitLockFileName  = "aphrollo-git.lock"
	gitOwnerFileName = "aphrollo-git.owner"
)

// gitMutatingVerbs are git subcommands that always mutate the index/repo
// state regardless of their own flags (task A11 requirement 2).
var gitMutatingVerbs = map[string]bool{
	"add":         true,
	"commit":      true,
	"merge":       true,
	"checkout":    true,
	"switch":      true,
	"reset":       true,
	"stash":       true,
	"rm":          true,
	"mv":          true,
	"rebase":      true,
	"cherry-pick": true,
	"revert":      true,
	"am":          true,
	"pull":        true,
}

// gitShimConfig bundles the shim's tunable knobs, mirroring cargoShimConfig
// so tests can shrink the wait budget/poll interval without touching
// production defaults.
type gitShimConfig struct {
	waitBudget   time.Duration
	pollInterval time.Duration
	realGit      string
}

// runTDDGit is the `aphrollo tdd git [git args...]` entry point: resolves
// the real git binary and the configured wait budget from the environment,
// then delegates to runGitShim (the testable core).
func runTDDGit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	realGit, err := resolveRealGit()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo tdd git: %v\n", err)
		return 1
	}
	cfg := gitShimConfig{
		waitBudget:   defaultGitWaitBudget,
		pollInterval: defaultGitPollInterval,
		realGit:      realGit,
	}
	if raw := strings.TrimSpace(os.Getenv("APHROLLO_GIT_WAIT_SECS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			cfg.waitBudget = time.Duration(n) * time.Second
		}
	}
	return runGitShim(args, stdin, stdout, stderr, cfg)
}

// runGitShim is the shim's testable core. Re-entrancy (requirement 4): if
// this process is itself already inside a locked git/cargo invocation
// (APHROLLO_GIT_QUEUED or APHROLLO_BUILD_LOCK_HELD in the environment), or
// the verb is read-only, it passes straight through untouched -- no lock,
// no repo-dir resolution, no output. Otherwise it resolves the repo's
// common git dir, and for a MUTATING verb only, acquires the per-repo lock
// (task A11 requirement 3) before running.
func runGitShim(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg gitShimConfig) int {
	if os.Getenv(tdd.GitQueuedEnv) == "1" || os.Getenv(tdd.BuildLockHeldEnv) == "1" {
		return execGit(cfg.realGit, args, stdin, stdout, stderr)
	}

	_, rest := gitGlobalArgs(args)
	if !isGitMutatingVerb(rest) {
		return execGit(cfg.realGit, args, stdin, stdout, stderr)
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown cwd)"
	}
	commonDir, ok := gitCommonDir(cfg.realGit, args, cwd)
	if !ok {
		// Not inside a repo (or git itself failed to resolve one) -- let
		// the real git report its own error, unlocked.
		return execGit(cfg.realGit, args, stdin, stdout, stderr)
	}

	lockPath := commonDir + "/" + gitLockFileName
	ownerPath := commonDir + "/" + gitOwnerFileName
	indexLockPath := commonDir + "/index.lock"

	// ONE combined deadline and ONE "have we printed the queued line yet"
	// flag cover BOTH phases (the advisory lock and, once that's ours, a
	// stray index.lock left by a git process that bypassed the shim
	// entirely) -- requirement 3 reads as one waiting experience, not two
	// independently-announced ones.
	start := time.Now()
	printedQueued := false
	for {
		release, acquired := tdd.TryAcquireFileLock(lockPath)
		heldByOther := !acquired
		if acquired && indexLockPresent(indexLockPath) {
			// We hold the advisory lock, but a git process that bypassed
			// the shim (found the real git first on PATH, or ran without
			// the shim dir prepended) is mid-write on index.lock directly.
			// Release and keep polling -- our advisory lock does nothing
			// to protect against that process; only index.lock's own
			// disappearance does.
			release()
			heldByOther = true
		}
		if !heldByOther {
			if printedQueued {
				fmt.Fprintln(stderr, gitAcquiredLine(time.Since(start)))
			}
			return runGitWithLock(release, ownerPath, cfg.realGit, args, stdin, stdout, stderr)
		}

		if !printedQueued {
			fmt.Fprintln(stderr, gitQueuedLine(ownerPath, indexLockPath))
			printedQueued = true
		}
		elapsed := time.Since(start)
		if elapsed >= cfg.waitBudget {
			fmt.Fprintln(stderr, gitGiveUpLine(elapsed, ownerPath))
			return exGitTempFail
		}
		time.Sleep(cfg.pollInterval)
	}
}

// indexLockPresent reports whether path exists -- best-effort, any stat
// error (including "not found") reads as absent.
func indexLockPresent(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runGitWithLock writes the owner file, runs the real git, then removes the
// owner file and releases the lock -- shortest possible hold, same
// ordering as cargo's runWithLock.
func runGitWithLock(release func(), ownerPath, realGit string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	defer release()
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown cwd)"
	}
	tdd.WriteFileLockOwner(ownerPath, "git "+strings.Join(args, " "), cwd)
	defer tdd.RemoveFileLockOwner(ownerPath)
	return execGit(realGit, args, stdin, stdout, stderr)
}

// gitGlobalArgs splits args into git's own global options (anything before
// the verb: -C dir, -c k=v, --git-dir path, --work-tree path, --no-pager,
// ... ) and the rest (verb + its own args). Paired-value options consume
// their following token too; every other leading "-"-prefixed token is
// treated as a standalone global flag. This does not attempt git's full CLI
// grammar -- good enough to find the verb and to forward the SAME prefix
// unchanged to a real-git rev-parse call.
func gitGlobalArgs(args []string) (prefix, rest []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break
		}
		prefix = append(prefix, a)
		i++
		switch a {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace":
			if i < len(args) {
				prefix = append(prefix, args[i])
				i++
			}
		}
	}
	return prefix, args[i:]
}

// isGitMutatingVerb classifies rest (args with any leading global options
// already stripped, per gitGlobalArgs) as index-mutating or not (task A11
// requirement 2). restore/apply/worktree are conditional on their own
// flags/sub-verb; everything else is a fixed lookup in gitMutatingVerbs.
func isGitMutatingVerb(rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	verb := rest[0]
	switch verb {
	case "restore":
		return containsToken(rest[1:], "--staged")
	case "apply":
		return containsToken(rest[1:], "--index") || containsToken(rest[1:], "--cached")
	case "worktree":
		if len(rest) < 2 {
			return false
		}
		sub := rest[1]
		return sub == "add" || sub == "remove"
	default:
		return gitMutatingVerbs[verb]
	}
}

// containsToken reports whether needle appears as an EXACT element of args
// -- not a prefix/substring match, since e.g. "--staged" and
// "--staged-with-tree" are different flags.
func containsToken(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}

// gitCommonDir resolves the repo's common git dir (shared by the main
// worktree and every linked worktree) by asking the REAL git, honoring
// whatever global options (-C, --git-dir, --work-tree, ...) preceded the
// verb in the original invocation -- rather than reimplementing git's own
// directory-resolution rules. Returns ok=false when not inside a repo (or
// on any other git failure), in which case the caller runs args unlocked
// and lets the real git report its own error.
func gitCommonDir(realGit string, args []string, cwd string) (string, bool) {
	prefix, _ := gitGlobalArgs(args)
	rpArgs := append(append([]string{}, prefix...), "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd := exec.Command(realGit, rpArgs...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", false
	}
	return dir, true
}

// resolveRealGit resolves the ACTUAL git binary the shim must run -- never
// via a bare PATH lookup, which would find the shim itself when the queue
// dir precedes Git's own cmd/bin dir on PATH. APHROLLO_REAL_GIT overrides
// everything (tests set this); otherwise the two locations Git for Windows
// installs to are probed in order.
func resolveRealGit() (string, error) {
	if override := os.Getenv("APHROLLO_REAL_GIT"); override != "" {
		return override, nil
	}
	candidates := []string{
		`C:\Program Files\Git\cmd\git.exe`,
		`C:\Program Files\Git\bin\git.exe`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("resolve git: none of %v found (set APHROLLO_REAL_GIT to override)", candidates)
}

// execGit runs the real git binary with args, inheriting stdio and the
// current process's environment/cwd, propagating its exit code.
// APHROLLO_GIT_QUEUED=1 rides along in the child's environment (belt and
// braces alongside precommit.go's cleanGitEnv, requirement 4), so any git
// subprocess THIS git process spawns (a hook, an alias) that happens to
// resolve back through the shim via PATH passes straight through instead of
// deadlocking on the lock this invocation may currently hold.
// execGitHookForTest, when set, is called with the resolved argv
// immediately before execGit spawns the real git process -- test-only
// instrumentation mirroring cargo_shim.go's execCargoHookForTest. Always
// nil in production.
var execGitHookForTest func(args []string)

func execGit(realGit string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if execGitHookForTest != nil {
		execGitHookForTest(args)
	}
	cmd := exec.Command(realGit, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	fmt.Fprintf(stderr, "aphrollo tdd git: %v\n", err)
	return 1
}

// gitQueuedLine composes the ONE-SHOT "queued behind" message printed the
// moment the shim first discovers it must wait -- naming the holder from
// the owner file when it's readable. When the owner file can't be read but
// index.lock exists anyway (requirement 3's fallback: a git process that
// bypassed the shim entirely, so no owner file was ever written), that gets
// its own more specific message instead of the generic "holder unknown"
// one.
func gitQueuedLine(ownerPath, indexLockPath string) string {
	o, ok := tdd.ReadFileLockOwner(ownerPath)
	if !ok {
		if indexLockPresent(indexLockPath) {
			return "git: waiting for .git/index.lock held by another git process"
		}
		return "git: queued behind another git process (holder unknown) -- waiting for the repo lock"
	}
	return fmt.Sprintf("git: queued behind %q in %s (pid %d, held %s)", o.Cmd, o.Cwd, o.PID, formatMinSec(time.Since(o.Started)))
}

// gitAcquiredLine composes the ONE-SHOT message printed once the shim
// acquires the lock AFTER having had to wait for it.
func gitAcquiredLine(waited time.Duration) string {
	return fmt.Sprintf("git: lock acquired after %ds", int(waited.Seconds()+0.5))
}

// gitGiveUpLine composes the message printed when the shim stops waiting
// without ever acquiring the lock.
func gitGiveUpLine(elapsed time.Duration, ownerPath string) string {
	holder := "(unknown)"
	if o, ok := tdd.ReadFileLockOwner(ownerPath); ok {
		holder = fmt.Sprintf("%q in %s (pid %d)", o.Cmd, o.Cwd, o.PID)
	}
	return fmt.Sprintf("git: gave up after %ds waiting for the repo lock (holder: %s)", int(elapsed.Seconds()+0.5), holder)
}
