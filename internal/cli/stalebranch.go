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

	// The apparent deletion is exactly what a stale branch produces when
	// trunk moved without the lane merging it back in -- but GitHub diffs a
	// PR against its MERGE BASE, never against trunk's current tip, so that
	// "deletion" never reaches the PR and never reaches trunk. A clean trial
	// merge is the direct proof: nothing this push carries actually removes
	// the stale paths from the tree trunk would have after taking it.
	if staleBranchMergeIsClean(realGit, workDir, trunk, branch) {
		return ""
	}

	remote := trunkRemote(realGit, workDir, trunk)
	return fmt.Sprintf(
		"gate: this push's diff against %s deletes paths this lane never touched: %s\n"+
			"  %s gained these after this lane branched -- that reads as a deletion this push is proposing, not one it made.\n"+
			"  run `git fetch %s && git merge %s` here, then re-check, before pushing.",
		trunk, strings.Join(stale, ", "), trunk, remote, trunk)
}

// staleBranchMergeIsClean reports whether trunk merges into branch's tip
// without conflict -- git's own trial merge (`merge-tree --write-tree`,
// git >= 2.38), never a re-implementation of one. It exits 0 for a clean
// result and 1 when the trial merge hits a real content conflict (verified
// against real git: two branches editing the same line exits 1; two
// branches each only adding their own file, the stale-deletion shape this
// check exists for, exits 0). Any other failure (git too old for the flag,
// an I/O error) reads as "could not confirm safety" and keeps the existing
// refusal, the same fail-toward-refusing-only-here direction this one call
// takes -- every OTHER uncertainty in this file fails open, but this is the
// one call whose whole job is proving safety, so its own failure proves
// nothing.
func staleBranchMergeIsClean(realGit, workDir, trunk, branch string) bool {
	cmd := exec.Command(realGit, "merge-tree", "--write-tree", trunk, branch)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	return cmd.Run() == nil
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
