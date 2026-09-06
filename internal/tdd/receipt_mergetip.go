package tdd

import (
	"os"
	"strings"
)

// mergeTip is the commit being merged IN — the tree a mutation run measured,
// and a revision the base check can still resolve.
type mergeTip struct {
	Rev, Tree, From string
}

// reflogActionEnv is what git tells a hook it is doing: during a merge it
// reads `merge <ref>`. It is the ONLY signal a clean automerge gives, because
// pre-merge-commit fires BEFORE .git/MERGE_HEAD is written — that file exists
// only for a conflicted or --no-commit merge. Reading it from this process's
// own environment is safe: cleanGitEnv scrubs GIT_* from the CHILD git's
// environment, never from ours.
const reflogActionEnv = "GIT_REFLOG_ACTION"

// mergeTipOf names the lane tip of the merge in progress, preferring
// MERGE_HEAD (when it exists it IS the merge) and falling back to the branch
// git says it is merging.
func mergeTipOf(repoRoot string) (mergeTip, bool) {
	if tree, ok := revTree(repoRoot, "MERGE_HEAD"); ok {
		return mergeTip{Rev: "MERGE_HEAD", Tree: tree, From: "MERGE_HEAD"}, true
	}
	if rev := reflogMergeRev(); rev != "" {
		if tree, ok := revTree(repoRoot, rev); ok {
			return mergeTip{Rev: rev, Tree: tree, From: reflogActionEnv}, true
		}
	}
	return mergeTip{}, false
}

// revTree resolves a revision's tree. `<rev>:` names it, and unlike
// `<rev>^{tree}` it survives any cmd.exe wrapper on the way to git — a caret
// is an escape character there.
func revTree(repoRoot, rev string) (string, bool) {
	out, err := git(repoRoot, "rev-parse", rev+":")
	if err != nil {
		return "", false
	}
	tree := strings.TrimSpace(out)
	return tree, tree != ""
}

// reflogMergeRev is the ref named by `merge <ref>`, "" for any other action.
func reflogMergeRev() string {
	f := strings.Fields(os.Getenv(reflogActionEnv))
	if len(f) < 2 || f[0] != "merge" {
		return ""
	}
	return f[1]
}

// mergeTipTree is the tree of the commit being merged IN, which is what the
// mutation run measured. "" when nothing names a merge.
func mergeTipTree(repoRoot string) string {
	tip, _ := mergeTipOf(repoRoot)
	return tip.Tree
}

// catchUpMerge reports whether the merge in progress is one a mutation receipt
// has nothing to say about, and why.
//
// Two shapes qualify, and both are "nothing new is arriving on main":
//
//	main into a lane — HEAD is not the repo's main branch, so whatever is
//	   being merged is already reviewed, merged code arriving in a working
//	   branch. The lane still owes a receipt when IT lands.
//	an already-contained branch — MERGE_HEAD is an ancestor of main, so main
//	   already holds every commit in it.
//
// A repo with no main branch at all is never a catch-up: the gate does not
// guess which branch is authoritative.
func catchUpMerge(repoRoot string) (string, bool) {
	main, ok := mainBranchRef(repoRoot)
	if !ok {
		return "", false
	}
	head := strings.TrimSpace(gitOut(repoRoot, "rev-parse", "--abbrev-ref", "HEAD"))
	if head != "" && head != "HEAD" && !isDefaultBranch(head) {
		return "merging into " + head + ", not " + main, true
	}
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		return "", false
	}
	if _, err := git(repoRoot, "merge-base", "--is-ancestor", tip.Rev, main); err == nil {
		return main + " already contains what is being merged", true
	}
	return "", false
}

// mainBranchRef names the branch a receipt gates entry to, preferring the
// remote's: `origin/main` is what a lane is actually measured against.
func mainBranchRef(repoRoot string) (string, bool) {
	for _, ref := range []string{"main", "master"} {
		if _, err := git(repoRoot, "rev-parse", "--verify", "--quiet", ref); err == nil {
			return ref, true
		}
	}
	return "", false
}
