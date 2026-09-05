package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A lane that branched before another PR landed on trunk pushes a diff that,
// against trunk's CURRENT tip, reads as deleting whatever that other PR
// added -- not because the lane means to remove it, but because the lane's
// own tree never saw it (issue #266: PR #264's own change was a three-line
// test fix, but its diff against main read "5 files changed, 15 insertions,
// 283 deletions" -- it would have erased the box-wide mutation-run lock
// #253 had merged minutes earlier). GitHub renders a PR against CURRENT
// main, so the absence reads as a deletion the PR is proposing; nothing
// mechanical checked for it before this.
//
// Deliberately cheaper than trunkMergePreviewStage's full merge-and-vet
// preview (#244): one diff, not a build, so it can never produce a false
// green from a preview that happens to compile, and it never fights a
// GENUINE deletion -- a lane that means to remove a file has touched it.

// staleBranchRefusalLine returns the one-line refusal for a push whose diff
// against trunk deletes a path this lane's own commits never touched, ""
// when the push is fine or the question could not be answered. Fail-open on
// every uncertainty, the same discipline trunkMergePreviewStage uses: no
// trunk name, a detached HEAD, this checkout IS trunk, an unresolvable
// merge-base, or any git error all read as "nothing to say", never a
// refusal -- a check that blocks a push because it could not tell is worse
// than the bug it exists to catch.
func staleBranchRefusalLine(realGit string, rest []string, workDir string) string {
	if len(rest) == 0 || rest[0] != "push" {
		return ""
	}
	if pushRemote(rest[1:]) == "" {
		return "" // delete, mirror, or an explicit refspec: not this check's business
	}
	branch, err := staleBranchGit(realGit, workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		return "" // detached HEAD
	}
	trunk := tdd.TrunkBranch(workDir)
	if trunk == "" {
		return ""
	}
	if branch == trunk || branch == strings.TrimPrefix(trunk, "origin/") {
		return "" // this checkout IS trunk: nothing to compare against
	}
	base, err := staleBranchGit(realGit, workDir, "merge-base", "HEAD", trunk)
	if err != nil {
		return ""
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}

	stale, err := staleBranchDeletions(realGit, workDir, trunk, base)
	if err != nil || len(stale) == 0 {
		return ""
	}

	remote := trunkRemote(realGit, workDir, trunk)
	return fmt.Sprintf(
		"gate: this push's diff against %s deletes paths this lane never touched: %s\n"+
			"  %s gained these after this lane branched -- that reads as a deletion this push is proposing, not one it made.\n"+
			"  run `git fetch %s && git merge %s` here, then re-check, before pushing.",
		trunk, strings.Join(stale, ", "), trunk, remote, trunk)
}

// staleBranchDeletions is the evidence staleBranchRefusalLine names: paths
// present in trunk, absent from this lane's HEAD, and absent from the union
// of every change this lane's own commits made since branching at base. A
// path the lane genuinely means to delete is in that union, so it is never
// flagged here.
func staleBranchDeletions(realGit, workDir, trunk, base string) ([]string, error) {
	deletedOut, err := staleBranchGit(realGit, workDir, "diff", "--diff-filter=D", "--name-only", trunk, "HEAD")
	if err != nil {
		return nil, err
	}
	deleted := nonEmptyLines(deletedOut)
	if len(deleted) == 0 {
		return nil, nil
	}
	touchedOut, err := staleBranchGit(realGit, workDir, "diff", "--name-only", base, "HEAD")
	if err != nil {
		return nil, err
	}
	touched := make(map[string]bool, len(deleted))
	for _, f := range nonEmptyLines(touchedOut) {
		touched[f] = true
	}
	var stale []string
	for _, f := range deleted {
		if !touched[f] {
			stale = append(stale, f)
		}
	}
	return stale, nil
}

// nonEmptyLines splits git's newline-delimited --name-only output, dropping
// blank lines (the trailing one every such output carries).
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// staleBranchGit runs realGit in workDir and hands back stdout, marked
// already-queued so it never waits on the per-repo lock this invocation may
// itself be holding.
func staleBranchGit(realGit, workDir string, args ...string) (string, error) {
	cmd := exec.Command(realGit, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	out, err := cmd.Output()
	return string(out), err
}

// trunkRemote names the remote to fetch from in the refusal's remedy. A trunk
// spelled `origin/main` carries its own remote; a trunk spelled `main` does
// not, and taking the branch name as the remote produced `git fetch main`,
// which fails with "'main' does not appear to be a git repository". A remedy
// that errors when typed is worse than no remedy, because acting on it is the
// only reason the line exists.
func trunkRemote(realGit, workDir, trunk string) string {
	if i := strings.IndexByte(trunk, '/'); i >= 0 {
		return trunk[:i]
	}
	if out, err := staleBranchGit(realGit, workDir, "config", "--get", "branch."+trunk+".remote"); err == nil {
		if r := strings.TrimSpace(out); r != "" {
			return r
		}
	}
	return "origin"
}
