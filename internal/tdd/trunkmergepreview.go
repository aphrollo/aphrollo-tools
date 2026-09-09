package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
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

	// Cheapest sharpest answer first: a law the COMBINATION breaks is read
	// off the merged tree without building anything.
	started := time.Now()
	if breaks := trunkPreviewLawBreaks(repoRoot, wt, trunkTip); len(breaks) > 0 {
		fmt.Fprintf(os.Stderr, "gate %s: trunk-preview ratchet in %s → blocked\n", gateName, wt)
		appendGateLog(gateName, repoRoot, "ratchet check (trunk merge preview)", "trunk-preview-law-blocked", time.Since(started))
		return GateResult{Blocked: true, Message: fmt.Sprintf(
			"gate %s: merging %s into this lane would break a law neither tree breaks alone — %s has moved past this lane's merge-base.\n"+
				"  %s\n"+
				"  run `git fetch && git merge %s` here and fix the combined tree before pushing; neither side is wrong on its own.",
			gateName, trunk, trunk, strings.Join(breaks, "\n  "), trunk)}
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

// trunkPreviewLawBreaks judges the merged preview tree against the consuming
// repo's own laws — the half `go vet` cannot see — and names every finding
// this lane must answer for, nil when there is none. A law is not a compiler
// error: escape #596 is two commits that each grew the same TestMain, each
// clean under test_main_exit alone, whose merged tree put `m.Run(` outside
// the law's window and was red on main from the moment it landed. The tree
// that broke it is exactly the one this stage has already built.
//
// Only a law the COMBINATION breaks belongs to this lane. A law trunk
// ALREADY breaks would otherwise refuse every commit in every lane until
// somebody else fixed main — a block no lane can clear, which is the worst
// kind — so the trunk tip is judged on its own (only on this rare refusing
// path, where the second scan costs nothing anybody waits for routinely) and
// whatever it already breaks is subtracted.
//
// Fail-open on every uncertainty, same as the rest of this stage: an
// unreadable tree, a check that errors, a worktree that will not build. The
// merged-tree-ratchet CI job on main is the backstop that still says so.
func trunkPreviewLawBreaks(repoRoot, wt, trunkTip string) []string {
	if !ratchet.HasLaws(wt) {
		return nil
	}
	preview, err := checkWholeTree(wt)
	if err != nil || !preview.Blocked() {
		return nil
	}
	already, err := lawsDeniedAt(repoRoot, trunkTip)
	if err != nil {
		return nil
	}
	var combination ratchet.Result
	for _, f := range preview.Findings {
		if f.Severity == ratchet.Deny.String() && !already[f.Law] {
			combination.Findings = append(combination.Findings, f)
		}
	}
	if !combination.Blocked() {
		return nil
	}
	return combination.Lines()
}

// checkWholeTree runs the law engine over one checkout exactly as `ratchet
// check --no-tighten --no-cache` does: judgement only, no baseline written,
// no scan cache shared with the box's own gate — the tree being judged is a
// throwaway worktree that will not exist a second time.
func checkWholeTree(root string) (ratchet.Result, error) {
	return ratchetCheckFn(ratchet.Options{
		Root:           root,
		Tracked:        trackedFiles(root),
		TrackedIgnored: trackedIgnoredFiles(root),
	})
}

// lawsDeniedAt names every law a deny-severity finding fires for in the tree
// at commit, judged in its own throwaway worktree. The error is the caller's
// signal that it cannot tell whose red this is, which it answers by allowing.
func lawsDeniedAt(repoRoot, commit string) (map[string]bool, error) {
	wt, err := os.MkdirTemp("", "gate-trunkonly-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(wt)
	if _, err := git(repoRoot, "worktree", "add", "--detach", wt, commit); err != nil {
		return nil, fmt.Errorf("worktree at %s: %w", commit, err)
	}
	defer func() { _, _ = git(repoRoot, "worktree", "remove", "--force", wt) }()

	res, err := checkWholeTree(wt)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, f := range res.Findings {
		if f.Severity == ratchet.Deny.String() {
			out[f.Law] = true
		}
	}
	return out, nil
}

// branchIsTrunk reports whether branch (a local branch name) names the same
// branch trunk resolved to, which may carry a "origin/" prefix of its own
// (trunkBranch's remote-HEAD route) that a local branch name never has.
func branchIsTrunk(branch, trunk string) bool {
	return branch == trunk || branch == strings.TrimPrefix(trunk, "origin/")
}
