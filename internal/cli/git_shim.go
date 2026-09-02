package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// gitLockScope is WHICH git directory a verb's lock belongs in. The index
// is per worktree (`index.lock` lives in that worktree's own git dir), so
// keying every mutation on the shared common dir made one lane's commit gate
// -- which holds its lock for the whole gate run -- block `git add` in every
// other worktree of the same repo.
type gitLockScope int

const (
	gitNoLock gitLockScope = iota
	// gitWorktreeScope: the invocation mutates THIS worktree's index or
	// HEAD. Lock lives in `git rev-parse --git-dir`.
	gitWorktreeScope
	// gitRepoScope: the invocation mutates state every worktree of the repo
	// shares -- branches, the worktree registry, the object store. Lock
	// lives in `git rev-parse --git-common-dir`.
	gitRepoScope
)

// gitIndexVerbs mutate the invoking worktree's index/HEAD regardless of
// their own flags. `pull` and `merge` are here rather than in the shared set
// because what they contend for is the index they write into; git does its
// own ref locking underneath.
var gitIndexVerbs = map[string]bool{
	"add":         true,
	"commit":      true,
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
	"merge":       true,
	"pull":        true,
}

// gitSharedVerbs mutate state shared by every worktree of the repo.
var gitSharedVerbs = map[string]bool{
	"fetch": true,
	"push":  true,
	"gc":    true,
}

// gitBranchMutationFlags are the `git branch` forms that write refs; a bare
// `git branch` (or --list) only reads, and must never take a lock.
var gitBranchMutationFlags = map[string]bool{
	"-d": true, "-D": true, "--delete": true,
	"-m": true, "-M": true, "--move": true,
	"-c": true, "-C": true, "--copy": true,
}

// gitShimConfig bundles the shim's tunable knobs, mirroring cargoShimConfig
// so tests can shrink the wait budget/poll interval without touching
// production defaults.
type gitShimConfig struct {
	waitBudget   time.Duration
	pollInterval time.Duration
	realGit      string
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
	scope := gitLockScopeFor(rest)
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

	lockPath := lockDir + "/" + gitLockFileName
	ownerPath := lockDir + "/" + gitOwnerFileName
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
			code := runGitWithLock(release, ownerPath, cfg.realGit, args, stdin, stdout, stderr)
			return recoverRejectedMerge(rest, args, cwd, cfg.realGit, code, start, stderr)
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
// error (including "not found") reads as absent. An empty path is "no index
// lock to wait for" (a repo-scoped verb).
func indexLockPresent(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// mergeConcludeFlags are the `git merge` sub-forms that CONCLUDE or CANCEL an
// already in-progress merge rather than starting one. The recovery below
// must never fire for these -- an operator's own `git merge --abort` running
// through the shim must never itself get "recovered".
var mergeConcludeFlags = map[string]bool{"--abort": true, "--continue": true, "--quit": true}

// isPlainMerge reports whether rest is a `git merge` invocation that STARTS
// a merge, as opposed to one of mergeConcludeFlags.
func isPlainMerge(rest []string) bool {
	if len(rest) == 0 || rest[0] != "merge" {
		return false
	}
	for _, a := range rest[1:] {
		if mergeConcludeFlags[a] {
			return false
		}
	}
	return true
}

// mergeRejectedRecoveryLine is the one line printed when recoverRejectedMerge
// actually aborts a rejected automerge -- exact text, since a session or a
// human reading the log matches on it.
const mergeRejectedRecoveryLine = "gate: merge rejected — aborted, checkout left clean; fix the cause and run the merge again"

// staleMarkerAge is how old a merge-rejected marker may get before it is
// reclaimed on sight, unconditionally: a marker left behind by a crashed or
// long-finished invocation is litter, not a signal any later merge should
// act on.
const staleMarkerAge = time.Hour

// recoverRejectedMerge is the fix for a real defect: when the pre-merge-
// commit gate rejects an automatic `git merge`, git still leaves MERGE_HEAD
// and the merged index in place ("Not committing merge; use 'git commit' to
// complete the merge."), which then refuses every OTHER session sharing the
// checkout ("You have not concluded your merge") until a human runs
// `git merge --abort`. The rejected marker (written by the gate itself,
// tdd.WriteMergeRejectedMarker, ONLY from the premergecommit subcommand) is
// the one signal narrow enough to recover automatically -- it must have been
// written by THIS invocation (its mtime not before start, so a leftover
// marker from an earlier, unrelated run is never mistaken for this one),
// MERGE_HEAD must now exist (the exact state the defect leaves), and there
// must be no unmerged path (a REAL conflict is a normal outcome the operator
// must resolve by hand, never auto-aborted). Any other outcome -- including
// `merge --abort` itself, and a merge that failed for an unrelated reason
// with no marker at all -- returns code untouched.
func recoverRejectedMerge(rest, args []string, cwd, realGit string, code int, start time.Time, stderr io.Writer) int {
	if code == 0 || !isPlainMerge(rest) {
		return code
	}
	workDir := gitWorkingDir(args, cwd)
	root := tdd.RepoRoot(workDir)
	if root == "" {
		return code
	}
	if !freshRejectionMarker(root, start) {
		return code
	}
	if !mergeHeadExists(realGit, workDir) {
		return code
	}
	if hasUnmergedPaths(realGit, workDir) {
		return code
	}
	_ = execGitQuiet(realGit, workDir, "merge", "--abort")
	_ = os.Remove(tdd.MergeRejectedMarkerPath(root))
	fmt.Fprintln(stderr, mergeRejectedRecoveryLine)
	return code
}

// freshRejectionMarker reports whether repoRoot has a merge-rejected marker
// written no earlier than start -- i.e. by THIS invocation's own child git,
// not a leftover from some earlier one. A marker older than staleMarkerAge
// is reclaimed silently here regardless of the verdict: it is litter no
// matter what caused this merge to fail.
func freshRejectionMarker(repoRoot string, start time.Time) bool {
	path := tdd.MergeRejectedMarkerPath(repoRoot)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) > staleMarkerAge {
		_ = os.Remove(path)
		return false
	}
	return !info.ModTime().Before(start)
}

// mergeHeadExists reports whether workDir currently has a MERGE_HEAD -- the
// state a rejected automerge leaves, and the state a real conflict leaves
// too, which is why this alone never decides recovery.
func mergeHeadExists(realGit, workDir string) bool {
	cmd := exec.Command(realGit, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	return cmd.Run() == nil
}

// hasUnmergedPaths reports whether workDir has any path git considers
// unmerged (diff-filter=U) -- a REAL conflict, as opposed to the clean,
// already-resolved index a rejected automerge leaves. Any error resolving
// this reads as "conflicts present": an unreadable answer must never license
// an automatic abort.
func hasUnmergedPaths(realGit, workDir string) bool {
	cmd := exec.Command(realGit, "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

// execGitQuiet runs realGit in workDir with the queued-child environment,
// discarding its output -- the recovery abort is not something a session
// needs to see the mechanics of, only the one summary line above.
func execGitQuiet(realGit, workDir string, args ...string) error {
	cmd := exec.Command(realGit, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	return cmd.Run()
}

// runGitWithLock writes the owner file, runs the real git, then removes the
// owner file and releases the lock -- shortest possible hold, same
// ordering as cargo's runWithLock.
func runGitWithLock(release func(), ownerPath, realGit string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown cwd)"
	}
	tdd.WriteFileLockOwner(ownerPath, "git "+strings.Join(args, " "), cwd)
	code := execGit(realGit, args, stdin, stdout, stderr)
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
	for i := 0; i < len(prefix)-1; i++ {
		if prefix[i] == "-C" {
			if filepath.IsAbs(prefix[i+1]) {
				dir = filepath.Clean(prefix[i+1])
			} else {
				dir = filepath.Join(dir, prefix[i+1])
			}
			i++
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

// gitLockScopeFor classifies rest (args with any leading global options
// already stripped, per gitGlobalArgs): no lock, the per-worktree index
// lock, or the repo-wide one. restore/apply/branch/worktree are conditional
// on their own flags or sub-verb; everything else is a fixed lookup.
func gitLockScopeFor(rest []string) gitLockScope {
	if len(rest) == 0 {
		return gitNoLock
	}
	switch verb := rest[0]; verb {
	case "restore":
		if containsToken(rest[1:], "--staged") {
			return gitWorktreeScope
		}
		return gitNoLock
	case "apply":
		if containsToken(rest[1:], "--index") || containsToken(rest[1:], "--cached") {
			return gitWorktreeScope
		}
		return gitNoLock
	case "worktree":
		if len(rest) < 2 {
			return gitNoLock
		}
		switch rest[1] {
		case "add", "remove", "prune":
			return gitRepoScope
		}
		return gitNoLock
	case "branch":
		for _, a := range rest[1:] {
			if gitBranchMutationFlags[a] {
				return gitRepoScope
			}
		}
		return gitNoLock
	default:
		if gitIndexVerbs[verb] {
			return gitWorktreeScope
		}
		if gitSharedVerbs[verb] {
			return gitRepoScope
		}
		return gitNoLock
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
