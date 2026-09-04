package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// precommitRootsThenTrunkPreview runs every staged root's own gate stages,
// then — only once every one of them has proven out — previews merging this
// lane into trunk (trunkMergePreviewStage). Kept out of precommit.go, which
// sits at its module_size baseline, and returns the zero GateResult (never
// Blocked) when nothing in either half blocked, so the caller's own
// `notes`-joining tail is unaffected either way.
func precommitRootsThenTrunkPreview(repoRoot string, groups []rootGroup, run SuiteRunner, collect func(GateResult) bool) GateResult {
	for _, g := range groups {
		if res := gateRoot("precommit", repoRoot, g, run, true); collect(res) {
			return res
		}
	}
	if res := trunkMergePreviewStage("precommit", repoRoot, run); collect(res) {
		return res
	}
	return GateResult{}
}

// trunkMergePreviewStage catches the case Mechanical's own doc comment
// names but nothing actually checked for before a push: "two branches that
// each individually passed Precommit can still integrate broken". The
// pre-merge-commit hook proves that for a LOCAL `git merge` onto the primary
// checkout, but a lane pushed straight to a PR never runs it — GitHub tests
// the lane against a synthetic merge with the CURRENT base branch, and nothing
// local ever built that combination first (issue #147: a lane's own tree
// vetted clean, referencing a function a concurrent trunk commit had renamed
// out from under it; CI's merge-preview caught it, the local gate never saw
// the two trees combined).
//
// This closes that gap the cheap way: when trunk has moved past this lane's
// merge-base, build the commit about to be made (the staged index, on top of
// its own HEAD — the same tree a `git commit` would create) in a throwaway
// worktree, merge trunk's current tip into it, and run `go vet ./...` there.
// A clean merge that still fails to vet is exactly what CI would have found
// after the push; this finds it before.
//
// Go-only, and fail-open on every uncertainty: no trunk name, no go.mod, an
// unmergeable (conflicted) combination, or a worktree/commit-tree step that
// could not run all ALLOW. This stage adds a check nothing else makes, so it
// must never become the reason an otherwise-sound commit is refused.
func trunkMergePreviewStage(gateName, repoRoot string, run SuiteRunner) GateResult {
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		return GateResult{}
	}
	trunk := trunkBranch(repoRoot)
	if trunk == "" {
		return GateResult{}
	}
	branch, err := git(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return GateResult{}
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" || branchIsTrunk(branch, trunk) {
		return GateResult{} // this checkout IS trunk: nothing to preview against
	}
	trunkTip, err := git(repoRoot, "rev-parse", trunk)
	if err != nil {
		return GateResult{}
	}
	trunkTip = strings.TrimSpace(trunkTip)
	base, err := git(repoRoot, "merge-base", "HEAD", trunk)
	if err != nil {
		return GateResult{}
	}
	if strings.TrimSpace(base) == trunkTip {
		return GateResult{} // trunk has not moved past this lane's merge-base
	}

	// The tree this commit is about to create, as a dangling commit object —
	// same trick indexTree/PostCommit's note use: `commit-tree` writes
	// nothing but an object no ref points at, so a stage that never blocks
	// leaves no trace.
	tree := indexTree(repoRoot)
	if tree == "" {
		return GateResult{}
	}
	candidate, err := git(repoRoot, "commit-tree", tree, "-p", "HEAD", "-m", "gate: trunk merge preview")
	if err != nil {
		return GateResult{}
	}
	candidate = strings.TrimSpace(candidate)

	wt, err := os.MkdirTemp("", "gate-trunkpreview-")
	if err != nil {
		return GateResult{}
	}
	defer os.RemoveAll(wt)
	if _, err := git(repoRoot, "worktree", "add", "--detach", wt, candidate); err != nil {
		return GateResult{}
	}
	defer func() { _, _ = git(repoRoot, "worktree", "remove", "--force", wt) }()

	if _, err := git(wt, "merge", "--no-commit", "--no-ff", trunkTip); err != nil {
		// A genuine conflict is a fact the developer already has to resolve
		// by hand; it says nothing about whether the CODE builds, so it is
		// not this stage's rejection to make.
		_, _ = git(wt, "merge", "--abort")
		return GateResult{}
	}

	vet := Runner{Cmd: "go", Args: []string{"vet", "./..."}, Dir: wt}
	res := run(vet, wt)
	if res.TimedOut || res.Passed {
		return GateResult{}
	}
	fmt.Fprintf(os.Stderr, "gate %s: trunk-preview go vet ./... in %s → blocked\n", gateName, wt)
	appendGateLog(gateName, repoRoot, cmdString(vet), "trunk-preview-blocked", res.Duration)
	return GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate %s: merging %s into this lane would not vet clean — %s has moved past this lane's merge-base.\n"+
			"  run `git fetch && git merge %s` (or rebase) here before pushing; nothing in this lane's own tree is wrong.\n%s",
		gateName, trunk, trunk, trunk, tailSnippet(res.Output))}
}

// branchIsTrunk reports whether branch (a local branch name) names the same
// branch trunk resolved to, which may carry a "origin/" prefix of its own
// (trunkBranch's remote-HEAD route) that a local branch name never has.
func branchIsTrunk(branch, trunk string) bool {
	return branch == trunk || branch == strings.TrimPrefix(trunk, "origin/")
}
