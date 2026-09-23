package tdd

import (
	"fmt"
	"os"
	"strings"
)

// rootGroup is one project root's staged Test/Source files (repo-root-
// relative paths), the unit both Precommit and Mechanical iterate.
type rootGroup struct {
	Root        string
	tests, srcs []string
}

// stagedRootGroups groups repoRoot's staged Source/Test files by
// FindProjectRoot — the single place both Precommit (fail-first +
// mechanical) and Mechanical (the merge gate: mechanical only) derive their
// per-root work from, so the grouping rule can never drift between them. A
// monorepo can stage files under several DIFFERENT project roots in one
// commit (a cargo workspace with a pytest tool living inside it); judging the
// whole commit by DetectRunner(repoRoot) alone means whichever marker sits at
// the outer repo root decides EVERY staged file's toolchain — a python-only
// commit under a Bevy-sized cargo workspace then pays a full `cargo nextest
// run` (20 minutes) for a change cargo has nothing to do with.
func stagedRootGroups(repoRoot string) []rootGroup {
	groups, _ := stagedRootGroupsErr(repoRoot)
	return groups
}

// stagedRootGroupsErr is stagedRootGroups with the index-read failure kept,
// for the gates that must tell "git says nothing is staged" from "git could
// not be asked".
func stagedRootGroupsErr(repoRoot string) ([]rootGroup, error) {
	staged, err := stagedFilesErr(repoRoot)
	if err != nil {
		return nil, err
	}
	if len(staged) == 0 {
		return nil, nil
	}
	tests, srcs := splitKinds(staged)
	all := append(append([]string{}, tests...), srcs...)
	var groups []rootGroup
	for _, root := range stagedProjectRoots(repoRoot, all) {
		groups = append(groups, rootGroup{
			Root:  root,
			tests: filesUnderRoot(repoRoot, root, tests),
			srcs:  filesUnderRoot(repoRoot, root, srcs),
		})
	}
	return groups, nil
}

// unreadableIndexMessage is what a gate says when git could not tell it what
// is staged. The distinction is the whole point: a gate that cannot read the
// index does not know whether this change was proven, and the one verdict it
// must never reach is the one it would have reached had everything worked.
func unreadableIndexMessage(gateName string, err error) string {
	return fmt.Sprintf("gate %s: cannot read the staged index — %v. "+
		"An unreadable index is not an empty one: the gate cannot tell what this change touches, "+
		"so every per-root stage would be skipped on no evidence. Fix the git failure "+
		"(an index.lock left behind by another process, a stale GIT_DIR inherited by the hook, "+
		"a git shim that failed to resolve) and retry.", gateName, err)
}

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
func precommitDecide(repoRoot string, run SuiteRunner) GateResult {
	// Concluding a CONFLICTED merge/cherry-pick/revert with `git commit`
	// fires git's pre-commit hook (pre-merge-commit only fires for an
	// AUTOMATIC, conflict-free merge commit) — task A10. Judging the WHOLE
	// lane diff against HEAD with fail-first is meaningless here (the
	// individual commits being merged already went through their own
	// fail-first when authored) and can cost many minutes of throwaway
	// worktree builds for nothing. Run EXACTLY the pre-merge routine
	// instead: Mechanical only, no fail-first, no anti-cheat.
	if ref := mergeInProgressRef(repoRoot); ref != "" {
		fmt.Fprintf(os.Stderr, "gate precommit: merge in progress (%s) — running the pre-merge routine (mechanical only)\n", ref)
		return Mechanical(repoRoot, run)
	}

	// A change with no code, or whose code changed only in comments, answers
	// to the tree guards and nothing else; see docsonly.go and
	// commentonly.go. StagedFastPath is also what the agreement test holds
	// CI's classify-diff to. It comes before the guards themselves only so
	// the log says which route the commit took.
	switch StagedFastPath(repoRoot) {
	case DiffDocsOnly:
		return docsOnlyFastPath("precommit", repoRoot)
	case DiffCommentOnly:
		return commentOnlyFastPath("precommit", repoRoot)
	}

	var notes []string
	collect := func(res GateResult) (blocked bool) {
		if res.Message != "" {
			notes = append(notes, res.Message)
		}
		return res.Blocked
	}
	// Cheapest first, and BEFORE the has-code check: a commit staging only a
	// baseline file or a doc is exactly the shape that raises a ceiling by
	// hand or lands a law regression, and it used to return here having
	// answered to nothing.
	if res := baselineStage("precommit", repoRoot); collect(res) {
		return res
	}
	if res := ratchetStage("precommit", repoRoot); collect(res) {
		return res
	}
	if res := docsCheckStage("precommit", repoRoot); collect(res) {
		return res
	}

	groups, err := stagedRootGroupsErr(repoRoot)
	if err != nil {
		return GateResult{Blocked: true, Message: unreadableIndexMessage("precommit", err)}
	}
	if len(groups) == 0 {
		return GateResult{Message: strings.Join(notes, "\n")}
	}

	// Anti-cheat: block a newly-INTRODUCED suppression before spending the suite
	// on it. Scoped to added lines, so a suppression that already lived in the
	// tree never blocks an unrelated later commit — only one this change adds.
	if msg := newSuppression(repoRoot); msg != "" {
		return GateResult{Blocked: true, Message: msg}
	}

	if res := precommitRootsThenTrunkPreview(repoRoot, groups, run, collect); res.Blocked {
		return res
	}
	return GateResult{Message: strings.Join(notes, "\n")}
}
