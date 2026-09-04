package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

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

// mergeStillMidMergeLinePrefix is the one line printed when the recovery
// abort itself FAILS: git refused to discard a worktree change it could not
// account for (the classic case is "error: Entry '<path>' not uptodate.
// Cannot merge." from a file modified underneath git after the merge staged
// it). Claiming success there is worse than saying nothing -- a session that
// trusts a false "checkout left clean" performs its next operation on top of
// a half-applied merge. abortOutput is git's own stderr, appended verbatim:
// it already names the path git refused to touch.
const mergeStillMidMergeLinePrefix = "gate: merge rejected — merge --abort FAILED, checkout is STILL MID-MERGE: "

// mergeStillMidMergeLine renders mergeStillMidMergeLinePrefix with git's own
// abort failure text and a recovery hint, so a session reading it can act
// deliberately instead of assuming a clean tree.
func mergeStillMidMergeLine(abortOutput string) string {
	detail := strings.TrimSpace(abortOutput)
	if detail == "" {
		detail = "(git printed no output)"
	}
	return mergeStillMidMergeLinePrefix + detail +
		"; resolve by hand (git checkout-index -f -- <path>, then merge --abort again, or conclude the merge)"
}

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
//
// The abort itself can fail: git refuses to discard a worktree change it
// cannot account for (the marker path was modified underneath it after the
// merge staged it). That refusal is correct; reporting it as success is not
// -- so the abort's own exit status decides which line is printed, and the
// marker is removed ONLY once the abort actually succeeded. On failure the
// marker (and MERGE_HEAD) are left exactly as git left them: the checkout is
// still mid-merge, and a later invocation must not silently retry on top of
// whatever caused the refusal.
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
	abortOutput, abortErr := execGitCaptureStderr(realGit, workDir, "merge", "--abort")
	if abortErr != nil {
		fmt.Fprintln(stderr, mergeStillMidMergeLine(abortOutput))
		return code
	}
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

// execGitCaptureStderr runs realGit in workDir with the queued-child
// environment, discarding stdout but capturing stderr: the recovery abort is
// not something a session needs to see the mechanics of when it SUCCEEDS,
// but when it FAILS its stderr is the one place that names the path git
// refused to touch, and swallowing it is exactly the defect this fixes.
func execGitCaptureStderr(realGit, workDir string, args ...string) (string, error) {
	cmd := exec.Command(realGit, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	return stderrBuf.String(), err
}
