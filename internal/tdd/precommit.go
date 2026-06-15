package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GateResult is the verdict of a git-time gate (precommit, prepush). A blocked
// action always carries a Message explaining what failed and how to proceed.
type GateResult struct {
	Blocked bool
	Message string
}

const failFirstMessage = "TDD fail-first: this commit adds tests AND implementation, but the new tests " +
	"PASS against the pre-edit code (HEAD) — so they are not actually pinning the new behavior. " +
	"A test that never went RED can't prove the implementation. Write the test first and watch it fail, " +
	"or split the test into its own earlier commit."

// Precommit runs the commit-time TDD wall in repoRoot:
//
//  1. Fail-first — ONLY when the staged change adds both tests and source.
//     The new tests are applied to a throwaway worktree at HEAD (without the
//     source change) and run; if they PASS there, they don't require the new
//     code, which is a fail-first violation. Triggering only on a combined
//     test+source commit is also the amendment fix: an impl-only amend stages
//     no test, so fail-first never re-judges already-committed tests.
//  2. Mechanical — the full suite must pass.
//
// Any inability to VERIFY fail-first (worktree/apply error) fails OPEN: the
// gate never blocks because its own tooling tripped. run is injected so the
// mechanical and worktree runs are testable.
func Precommit(repoRoot string, run SuiteRunner) GateResult {
	staged := stagedFiles(repoRoot)
	if len(staged) == 0 {
		return GateResult{}
	}
	tests, srcs := splitKinds(staged)

	if len(tests) > 0 && len(srcs) > 0 {
		if violated, conclusive := failFirstViolated(repoRoot, tests, run); conclusive && violated {
			return GateResult{Blocked: true, Message: failFirstMessage}
		}
	}

	runner, ok := DetectRunner(repoRoot)
	if ok {
		if res := run(runner, repoRoot); !res.Passed {
			return GateResult{Blocked: true, Message: "TDD mechanical: tests failing — fix before committing.\n" + snippet(res.Output)}
		}
	}
	return GateResult{}
}

// splitKinds partitions repo-relative staged paths into test and source files,
// ignoring everything else.
func splitKinds(paths []string) (tests, srcs []string) {
	for _, p := range paths {
		switch ClassifyFile(p) {
		case Test:
			tests = append(tests, p)
		case Source:
			srcs = append(srcs, p)
		}
	}
	return tests, srcs
}

// failFirstViolated builds a throwaway worktree at HEAD, applies ONLY the
// staged test changes, and runs the suite there. It returns (violated,
// conclusive): violated is true when the tests pass without the new source
// (they should fail first); conclusive is false when the check could not run,
// in which case the caller must not block.
func failFirstViolated(repoRoot string, tests []string, run SuiteRunner) (violated, conclusive bool) {
	wt, err := os.MkdirTemp("", "tdd-failfirst-")
	if err != nil {
		return false, false
	}
	defer os.RemoveAll(wt)
	if _, err := git(repoRoot, "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		return false, false
	}
	defer git(repoRoot, "worktree", "remove", "--force", wt)

	// The staged test diff applied onto HEAD: tests present, new source absent.
	diff, err := gitStaged(repoRoot, tests)
	if err != nil || strings.TrimSpace(diff) == "" {
		return false, false
	}
	if err := gitApply(wt, diff); err != nil {
		return false, false // can't reproduce the test state → don't block
	}

	runner, ok := DetectRunner(wt)
	if !ok {
		return false, false
	}
	res := run(runner, wt)
	// Tests PASS without the new source ⇒ they never went RED ⇒ violation.
	return res.Passed, true
}

// --- git plumbing (scrubbed environment) ------------------------------------

// cleanGitEnv strips every GIT_* variable from the environment. A git hook runs
// with GIT_DIR / GIT_INDEX_FILE / GIT_WORK_TREE / GIT_OBJECT_DIRECTORY and
// friends pointing at the OUTER repo; leaking any of them makes worktree
// commands operate on the wrong state. An allowlist (drop all GIT_*) is safer
// than blocklisting the few that were known to cause trouble.
func cleanGitEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// stagedFiles lists the added/copied/modified paths in the index, as repo-root-
// relative paths.
func stagedFiles(repoRoot string) []string {
	out, err := git(repoRoot, "diff", "--cached", "--name-only", "--diff-filter=ACM")
	if err != nil {
		return nil
	}
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// gitStaged returns the staged diff restricted to the given pathspecs.
func gitStaged(repoRoot string, paths []string) (string, error) {
	args := append([]string{"diff", "--cached", "--"}, paths...)
	return git(repoRoot, args...)
}

// gitApply applies a unified diff to a worktree via `git apply` on stdin.
func gitApply(wt, diff string) error {
	cmd := exec.Command("git", "apply", "--whitespace=nowarn")
	cmd.Dir = wt
	cmd.Env = cleanGitEnv()
	cmd.Stdin = strings.NewReader(diff)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git apply: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// RepoRoot returns the git top-level for dir, or "" if dir is not in a repo —
// the working directory a git pre-commit hook should evaluate.
func RepoRoot(dir string) string {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(out))
}
