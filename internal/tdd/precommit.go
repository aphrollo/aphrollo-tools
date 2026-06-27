package tdd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
//  2. Mechanical — the related tests for the staged source+test files must pass.
//     A commit that stages no source AND no test (docs/yaml only) skips this
//     stage entirely.
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

	// Anti-cheat: block a newly-INTRODUCED suppression before spending the suite
	// on it. Scoped to added lines, so a suppression that already lived in the
	// tree never blocks an unrelated later commit — only one this change adds.
	if msg := newSuppression(repoRoot); msg != "" {
		return GateResult{Blocked: true, Message: msg}
	}

	// Fail-first runs the new tests in a throwaway worktree at HEAD. That worktree
	// has no node_modules — worktrees don't share gitignored deps and we do NOT
	// `pnpm install` per commit (too slow) — so for vitest/jest repos the suite
	// can't run there and fail-first is effectively Go-only. It still fails OPEN
	// (an unrunnable suite is inconclusive, never a block).
	//
	// Zig fails OPEN here by construction, and deliberately so. Its tests are
	// `test "..." {}` blocks INLINE in src/*.zig, so an inline-test commit stages
	// only Source-classified .zig files (ClassifyFile flips only *_test.zig /
	// tests/*.zig to Test) — len(tests) is 0 and this guard skips fail-first.
	// That is correct: the test and the impl it exercises live in the SAME hunk
	// and cannot be cleanly separated, so applying "just the test" to a HEAD
	// worktree would drag the impl along and make the check meaningless. An
	// EXPLICIT tests/*.zig staged alongside its source DOES enter fail-first, but
	// an integration test cannot compile without the source it imports, so the
	// worktree run fails to RUN and falls through the unrunnable-suite path above
	// (Passed=false ⇒ violated=false). Either way fail-first never false-blocks a
	// Zig commit; the mechanical stage's full `zig build test` is the real gate.
	if len(tests) > 0 && len(srcs) > 0 {
		if violated, conclusive := failFirstViolated(repoRoot, tests, run); conclusive && violated {
			return GateResult{Blocked: true, Message: failFirstMessage}
		}
	}

	// Changes-gate: a docs/yaml-only commit (no staged source AND no staged test)
	// has nothing to test, so skip the mechanical stage. Anti-cheat above still
	// ran (it's cheap and only judges added lines).
	if len(tests) == 0 && len(srcs) == 0 {
		return GateResult{}
	}

	runner, ok := DetectRunner(repoRoot)
	if ok {
		// Scope the mechanical run to the related tests of the staged source+test
		// files: commit-time is a fast scoped check; CI runs the full suite at
		// submit as the authoritative gate. A runner with no related mode (or an
		// unknown command) falls back to the full suite unchanged.
		if scoped, narrowed := narrowToStaged(runner, append(append([]string{}, tests...), srcs...)); narrowed {
			runner = scoped
		}
		if res := run(runner, repoRoot); !res.Passed {
			return GateResult{Blocked: true, Message: "TDD mechanical: tests failing — fix before committing.\n" + snippet(res.Output)}
		}
	}
	return GateResult{}
}

// suppressionCommitHeader prefixes a commit-time anti-cheat block; the policy's
// own reason (naming the directive and the fix) follows.
const suppressionCommitHeader = "TDD anti-cheat: this commit introduces a suppression that silences a quality gate."

// newSuppression scans the lines this commit ADDS for a suppression and, on the
// first hit in a source/test file, returns the block message. Only added lines
// are judged, so a directive that already lived in the file does not block an
// unrelated commit. The check masks each file's full staged post-image and then
// restricts to the added line numbers, so the masking sees balanced
// string/comment context and a crafted multi-line edit cannot hide a later
// added directive behind an unbalanced opener. Returns "" when nothing blocks.
func newSuppression(repoRoot string) string {
	for _, fa := range stagedAdds(repoRoot) {
		switch ClassifyFile(fa.path) {
		case Source, Test:
		default:
			continue
		}
		post, err := git(repoRoot, "show", ":"+fa.path)
		if err != nil {
			continue // file not in the index (e.g. deletion) → nothing to judge
		}
		v := addedView(post, fa.added, langOf(fa.path))
		if d := evaluateView(v, suppressionPolicies, commitPhase); d.Action == Block {
			return suppressionCommitHeader + "\n  " + fa.path + ": " + d.Reason
		}
	}
	return ""
}

// fileAdd is the set of added line numbers (1-based, in the post-image) for one
// file in a staged diff.
type fileAdd struct {
	path  string
	added map[int]bool
}

// stagedAdds parses `git diff --cached -U0` into the added line NUMBERS per
// file, keyed by the `+++ b/<path>` header and skipping deletions
// (`+++ /dev/null`). Line numbers come from each hunk's `@@ … +start[,count] @@`
// new-side range, advanced as `+` lines are consumed, so the caller can mask the
// full post-image and judge only these lines — far more robust than the old
// approach of masking the deletion-stripped added-line text on its own.
func stagedAdds(repoRoot string) []fileAdd {
	out, err := git(repoRoot, "diff", "--cached", "--unified=0", "--no-color")
	if err != nil {
		return nil
	}
	var (
		adds  []fileAdd
		cur   string
		lines map[int]bool
		newNo int // next new-file line number within the current hunk
	)
	flush := func() {
		if cur != "" && len(lines) > 0 {
			adds = append(adds, fileAdd{path: cur, added: lines})
		}
		lines = nil
	}
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			flush()
			cur = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			lines = map[int]bool{}
		case strings.HasPrefix(line, "+++"): // +++ /dev/null (deletion)
			flush()
			cur = ""
		case strings.HasPrefix(line, "@@"):
			newNo = hunkNewStart(line)
		case strings.HasPrefix(line, "+"):
			if lines != nil && newNo > 0 {
				lines[newNo] = true
				newNo++
			}
		case strings.HasPrefix(line, "-"):
			// deletions don't advance the new-file line counter
		default:
			// context line (none at -U0) advances the new-file counter
			if newNo > 0 {
				newNo++
			}
		}
	}
	flush()
	return adds
}

// hunkNewStartRe captures the new-file start line from a `@@ -a,b +c,d @@`
// header — the digits right after the `+`, before any `,count`.
var hunkNewStartRe = regexp.MustCompile(`\+(\d+)`)

// hunkNewStart returns the new-file starting line of a `@@ -a,b +c,d @@` header,
// or 0 if it can't be parsed.
func hunkNewStart(header string) int {
	m := hunkNewStartRe.FindStringSubmatch(header)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
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
	defer func() { _, _ = git(repoRoot, "worktree", "remove", "--force", wt) }() // best-effort cleanup

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
	//
	// SuiteResult carries only Passed/Output, with no couldn't-run signal, so a
	// suite that failed to RUN (e.g. a vitest/jest worktree with no node_modules)
	// is indistinguishable from one that ran and failed: both surface as
	// Passed=false ⇒ (violated=false, conclusive=true). That mislabels a
	// non-running suite as a conclusive non-violation rather than inconclusive,
	// but it fails in the safe direction — non-violation never blocks — so the
	// gate stays fail-open. Correcting the label needs a distinct couldn't-run
	// signal on SuiteResult, which is left for a runner-contract change.
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
	return gitStdin(dir, nil, args...)
}

// gitStdin runs git in dir with a scrubbed environment, optionally feeding stdin
// (nil for none), and returns the combined output. It is the single place the
// exec/clean-env/CombinedOutput pattern lives.
func gitStdin(dir string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	cmd.Stdin = stdin
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
	if out, err := gitStdin(wt, strings.NewReader(diff), "apply", "--whitespace=nowarn"); err != nil {
		return fmt.Errorf("git apply: %s", strings.TrimSpace(out))
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
