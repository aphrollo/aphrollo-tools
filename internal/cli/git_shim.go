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

// gitShimConfig bundles the shim's tunable knobs, mirroring cargoShimConfig
// so tests can shrink the wait budget/poll interval without touching
// production defaults.
type gitShimConfig struct {
	waitBudget   time.Duration
	pollInterval time.Duration
	// indexLockGrace is how long an index.lock must sit unchanged before
	// the shim stops waiting on it; zero means defaultGitIndexLockGrace.
	indexLockGrace time.Duration
	realGit        string
}

// runGateGit is the `aphrollo tdd git [git args...]` entry point: resolves
// the real git binary and the configured wait budget from the environment,
// then delegates to runGitShim (the testable core).
func runGateGit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	realGit, err := resolveRealGit()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo tdd git: %v\n", err)
		return 1
	}
	cfg := gitShimConfig{
		waitBudget:     defaultGitWaitBudget,
		pollInterval:   defaultGitPollInterval,
		indexLockGrace: defaultGitIndexLockGrace,
		realGit:        realGit,
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
//
// #443: yes, this also skips every refusal below -- load-bearing (pinned by
// TestRunGitShim_PassthroughWhenGitQueuedEnvSet; the gate's own harness
// sets the var so a nested call skips a lock the outer run holds), not a
// sanctioned way past a refused push -- staleBranchMergeIsClean is that.
func runGitShim(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg gitShimConfig) int {
	if os.Getenv(tdd.GitQueuedEnv) == "1" || os.Getenv(tdd.BuildLockHeldEnv) == "1" {
		return execGit(cfg.realGit, args, stdin, stdout, stderr)
	}

	prefix, rest := gitGlobalArgs(args)

	// classifyRest is what primaryRefusalLine and gitLockScopeFor actually
	// switch on. Left as rest by default; resolved to a configured alias's
	// expansion below, ONCE, so `[alias] cob = checkout -b` classifies as
	// checkout -b rather than as the unrecognized verb "cob" (issue #279).
	classifyRest := rest
	shellAlias := false

	// The primary checkout is merge-only, and the refusal comes before both
	// the lock and git itself: a branch that already moved cannot be un-moved
	// by a message.
	if cwd, err := os.Getwd(); err == nil {
		workDir := gitWorkingDir(args, cwd)
		if expanded, isShell, found := resolveAlias(cfg.realGit, workDir, rest); found {
			shellAlias = isShell
			if !isShell {
				classifyRest = expanded
			}
		}
		// The three doors past the commit gate (issue #314): a verb-level
		// --no-verify/-n, or -c core.hooksPath in the global prefix. Refused
		// outright in the primary checkout; a lane's use is allowed but
		// logged, so `gate stats` sees every use of the hatch.
		if hooksBypassDoor(prefix, classifyRest) {
			if line := hooksBypassRefusalLine(prefix, classifyRest, workDir); line != "" {
				fmt.Fprintln(stderr, line)
				return 1
			}
			tdd.AppendGateLog("precommit", tdd.LogToken(cwd), tdd.LogToken(strings.Join(args, " ")), "override-no-verify", 0)
		}
		if line := primaryRefusalLine(cfg.realGit, classifyRest, workDir, shellAlias); line != "" {
			fmt.Fprintln(stderr, line)
			return 1
		}
		// Same "before the lock, before git runs" placement as the
		// primary-checkout refusal above: a push that already reached the
		// remote is not one this stage refused anything about (issue #266).
		if line := staleBranchRefusalLine(cfg.realGit, rest, workDir); line != "" {
			fmt.Fprintln(stderr, line)
			return 1
		}
		// A declared hand mutation proof restoring a file it held before
		// mutating it (#650). Before the discard wall, because the wall's
		// question ("what would this destroy") has a different answer for
		// this one command: the restore is the proof's PROTECTIVE step, and
		// the shim serves it from the held bytes rather than letting git
		// restore from the index over the lane's unstaged work.
		if code, handled := mutationProofRestore(rest, workDir, stderr); handled {
			return code
		}
		// The discard wall (#343): same placement again — a `reset --hard`
		// that already ran cannot be un-run by a refusal printed after it,
		// and the louder marker's notice (a line with refuse=false, naming
		// the unstaged work it is about to destroy) is worth nothing after
		// the destruction either, so both print here.
		if line, refuse := discardWallRefusal(cfg, rest, workDir); line != "" || refuse {
			fmt.Fprintln(stderr, line)
			if refuse {
				return 1
			}
		}
		// The shared-stash wall (#384): refs/stash is one ref for the whole
		// repo, not per-worktree — a pop that already took another lane's
		// entry cannot be undone by a refusal printed after it either.
		if line, refuse := stashRefusalLine(cfg.realGit, rest, workDir); refuse {
			fmt.Fprintln(stderr, line)
			return 1
		}
	}

	scope := gitLockScopeFor(classifyRest)
	if scope == gitNoLock {
		return execGit(cfg.realGit, args, stdin, stdout, stderr)
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown cwd)"
	}
	lockDir, ok := gitLockDir(cfg.realGit, args, cwd, scope)
	if !ok {
		// Not inside a repo (or git itself failed to resolve one) -- let
		// the real git report its own error, unlocked.
		return execGit(cfg.realGit, args, stdin, stdout, stderr)
	}

	lockPath := lockDir + "/" + gitLockFileFor(scope)
	ownerPath := lockDir + "/" + gitOwnerFileFor(scope)
	// index.lock lives in the worktree's OWN git dir, which is exactly the
	// directory a worktree-scoped lock already keys on. A repo-scoped verb
	// does not touch the index, so it has none to wait for.
	indexLockPath := lockDir + "/index.lock"
	if scope == gitRepoScope {
		indexLockPath = ""
	}

	// ONE combined deadline and ONE "have we printed the queued line yet"
	// flag cover BOTH phases (the advisory lock and, once that's ours, a
	// stray index.lock left by a git process that bypassed the shim
	// entirely) -- requirement 3 reads as one waiting experience, not two
	// independently-announced ones.
	start := time.Now()
	printedQueued := false
	var indexWatch gitIndexLockWatch
	for {
		release, acquired := tdd.TryAcquireFileLock(lockPath)
		heldByOther := !acquired
		blockedByIndexLock := acquired && indexLockPresent(indexLockPath)
		if blockedByIndexLock {
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
			// The merge recovery runs UNDER the lock: `git merge --abort`
			// rewrites the index and the worktree, so it needs exactly the
			// protection the merge itself had. Released first, it lands on
			// top of whatever the next session started the instant the
			// merge exited.
			code := runGitWithLock(release, ownerPath, cfg.realGit, args, stdin, stdout, stderr,
				func(code int) int {
					return recoverRejectedMerge(rest, args, cwd, cfg.realGit, code, start, stderr)
				})
			// The gate note does not ride along with a branch push, and a
			// note nobody pushed reaches no CI runner. Best effort: it
			// never changes the push's own exit code. OUTSIDE the lock, for
			// the same reason the disk sweep is: a network round trip is
			// not something every other session's git may queue behind.
			// The repo the push acted on, not the one this process happens
			// to stand in: `git -C <elsewhere> push` carries ITS note to ITS
			// remote, and reaching this checkout's remote instead is both the
			// wrong note and a network round trip nobody asked for.
			pushGateNotes(rest, gitWorkingDir(args, cwd), cfg.realGit, code, stderr)
			return code
		}

		if !printedQueued {
			fmt.Fprintln(stderr, gitQueuedLine(ownerPath, indexLockPath))
			printedQueued = true
		}
		// A lock nobody is writing to is the one thing this wait can never
		// outlast (issue #608): report it with its remedy in seconds rather
		// than spend the whole budget proving it silently.
		if line, stalled := indexWatch.stallLine(indexLockPath, cfg.indexLockGrace, blockedByIndexLock); stalled {
			fmt.Fprintln(stderr, line)
			return exGitTempFail
		}
		elapsed := time.Since(start)
		if elapsed >= cfg.waitBudget {
			fmt.Fprintln(stderr, gitGiveUpLine(elapsed, ownerPath))
			return exGitTempFail
		}
		time.Sleep(cfg.pollInterval)
	}
}

// runGitWithLock writes the owner file, runs the real git, then removes the
// owner file and releases the lock -- shortest possible hold, same
// ordering as cargo's runWithLock.
//
// underLock (nil for none) is follow-up work that must be as protected as
// the child git itself was -- it runs after the child exits and BEFORE the
// owner record is cleared and the lock released, and its verdict becomes
// the invocation's exit code. Anything that does NOT touch this repo's
// index belongs outside the lock instead: a network round trip or a
// multi-gigabyte disk sweep held here is a queue every other session joins.
func runGitWithLock(release func(), ownerPath, realGit string, args []string, stdin io.Reader, stdout, stderr io.Writer, underLock func(code int) int) int {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown cwd)"
	}
	tdd.WriteFileLockOwner(ownerPath, "git "+strings.Join(args, " "), cwd)
	code := execGit(realGit, args, stdin, stdout, stderr)
	if underLock != nil {
		code = underLock(code)
	}
	tdd.RemoveFileLockOwner(ownerPath)
	release()
	// AFTER the release: the sweep can RemoveAll tens of gigabytes, and doing
	// it under the lock made every other session's git queue behind a disk
	// cleanup.
	if code == 0 {
		sweepAfterWorktreeChange(args, cwd)
	}
	return code
}

// gcAfterWorktreeChange is the sweep the shim runs after a worktree
// removal, behind a variable so a test can observe it without a disk full
// of directories.
var gcAfterWorktreeChange = defaultGCAfterWorktreeChange

func defaultGCAfterWorktreeChange(repoRoot, removed string) int64 {
	return tdd.GCAfterWorktreeChange(repoRoot, removed)
}

// sweepAfterWorktreeChange reclaims what a SUCCESSFUL `git worktree
// remove`/`prune` left behind: git deletes the checkout and never the
// target/ inside it, so an abandoned lane's build dir -- routinely tens of
// gigabytes -- outlives the tree it belonged to with nothing pointing at it
// anymore. Silent: the operator asked to remove a worktree, not to read a
// report about disk space.
func sweepAfterWorktreeChange(args []string, cwd string) {
	removed, ok := worktreeSweepTargetFor(args, cwd)
	if !ok {
		return
	}
	root := tdd.RepoRoot(gitWorkingDir(args, cwd))
	if root == "" {
		return
	}
	gcAfterWorktreeChange(root, removed)
}

// worktreeSweepTargetFor resolves the sweep target the way GIT resolves the
// same argument: relative to `-C <dir>` when one is given. Resolving against
// the shim's own cwd instead pointed a RemoveAll at a path in a different
// tree entirely.
func worktreeSweepTargetFor(args []string, cwd string) (removed string, ok bool) {
	_, rest := gitGlobalArgs(args)
	return worktreeSweepTarget(rest, gitWorkingDir(args, cwd))
}

// gitWorkingDir is the directory a verb actually runs in: the last `-C dir`
// (git applies them cumulatively, left to right), else the shim's cwd.
func gitWorkingDir(args []string, cwd string) string {
	dir := cwd
	prefix, _ := gitGlobalArgs(args)
	// A flag's separate value is consumed by setting skip rather than by
	// stepping the index inside the loop: an in-loop step is a mutation site
	// whose decrement never terminates, which a mutation run can only report
	// as a timeout and never as a caught mutant.
	skip := false
	for i, p := range prefix {
		if skip {
			skip = false
			continue
		}
		if p != "-C" || i+1 >= len(prefix) {
			continue
		}
		val := prefix[i+1]
		skip = true
		if filepath.IsAbs(val) {
			dir = filepath.Clean(val)
		} else {
			dir = filepath.Join(dir, val)
		}
	}
	return dir
}

// worktreeSweepTarget reports whether rest is one of the two verbs that can
// orphan a build dir, and which tree it named: `worktree remove <path>`
// names one (resolved against cwd, since git accepts a relative path),
// `worktree prune` names none -- the scan finds them. `worktree add`
// creates, so it never qualifies.
func worktreeSweepTarget(rest []string, cwd string) (removed string, ok bool) {
	if len(rest) < 2 || rest[0] != "worktree" {
		return "", false
	}
	switch rest[1] {
	case "prune":
		return "", true
	case "remove":
		for _, a := range rest[2:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if filepath.IsAbs(a) {
				return filepath.Clean(a), true
			}
			return filepath.Join(cwd, a), true
		}
		return "", true
	}
	return "", false
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

// gitLockDir resolves which directory a scope's lock file lives in by
// asking the REAL git: the invoking worktree's own git dir for an index
// mutation, the dir every worktree shares for a repo-wide one.
func gitLockDir(realGit string, args []string, cwd string, scope gitLockScope) (string, bool) {
	flag := "--git-dir"
	if scope == gitRepoScope {
		flag = "--git-common-dir"
	}
	return gitRevParseDir(realGit, args, cwd, flag)
}

// gitRevParseDir asks the real git to resolve one directory flag, honoring
// whatever global options (-C, --git-dir, --work-tree, ...) preceded the
// verb in the original invocation rather than reimplementing git's own
// directory-resolution rules.
func gitRevParseDir(realGit string, args []string, cwd, flag string) (string, bool) {
	prefix, _ := gitGlobalArgs(args)
	rpArgs := append(append([]string{}, prefix...), "rev-parse", "--path-format=absolute", flag)
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
// via a bare PATH lookup on its own, which would find the shim itself when
// the queue dir precedes git's own bin dir on PATH. Three steps, in order:
// APHROLLO_REAL_GIT overrides everything (tests set this); then PATH is
// walked with any shim directory skipped (tdd.GitBinaryOnPath -- the SAME
// shim-skipping resolver internal/tdd's own gitBinary uses, cross-platform,
// so the two never drift into disagreeing about what "real git" means); only
// on Windows, as a last resort, the two locations Git for Windows installs
// to are probed directly -- an installed-but-not-on-PATH git is common
// there (a shell that has not re-read its profile since install) and has no
// equivalent on Linux/macOS, where a git absent from PATH is just absent.
func resolveRealGit() (string, error) {
	if override := os.Getenv("APHROLLO_REAL_GIT"); override != "" {
		return override, nil
	}
	if c, ok := tdd.GitBinaryOnPath(); ok {
		return c, nil
	}
	if runtime.GOOS == "windows" {
		candidates := []string{
			`C:\Program Files\Git\cmd\git.exe`,
			`C:\Program Files\Git\bin\git.exe`,
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
		return "", fmt.Errorf("resolve git: not on PATH, and none of %v found (set APHROLLO_REAL_GIT to override)", candidates)
	}
	return "", fmt.Errorf("resolve git: not on PATH (set APHROLLO_REAL_GIT to override)")
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
