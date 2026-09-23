package gitx

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
