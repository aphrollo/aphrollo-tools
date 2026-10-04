package failfirst

import (
	"path/filepath"
	"strings"

	igit "github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// A gate runs a pytest root in a throwaway worktree (the merged tree, the
// fail-first HEAD tree), where the root's gitignored virtualenv does not
// exist. The same root in the repo's long-lived worktrees usually has one, so
// the search extends to them: the primary checkout first, then the lane whose
// tip the merge worktree is merging, found by its MERGE_HEAD.

// worktreeEntry is one row of `git worktree list --porcelain`.
type worktreeEntry struct {
	path string
	head string
}

// parseWorktrees reads `git worktree list --porcelain`; the first entry is
// the primary checkout.
func parseWorktrees(porcelain string) []worktreeEntry {
	var out []worktreeEntry
	for _, line := range strings.Split(porcelain, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			out = append(out, worktreeEntry{path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "HEAD ") && len(out) > 0:
			out[len(out)-1].head = strings.TrimPrefix(line, "HEAD ")
		}
	}
	return out
}

// otherWorktreeRoots is the search for root: root itself and its worktree's
// top, then the same repo-relative path and top in the worktree whose HEAD is
// the merge being judged (the lane, whose venv matches the code being merged),
// then in the primary checkout, whose venv may be stale. A root outside any
// worktree searches only itself.
func otherWorktreeRoots(root string) pytestSearch {
	search := pytestSearch{root: root}
	top := gitx.RepoRoot(root)
	if top == "" {
		return search
	}
	search.ownTop = igit.Canonical(top)
	rel, err := filepath.Rel(top, igit.Canonical(root))
	if err != nil {
		return search
	}
	list, err := git(root, "worktree", "list", "--porcelain")
	if err != nil {
		return search
	}
	mergeHead, _ := git(root, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	mergeHead = strings.TrimSpace(mergeHead)
	var primaryDir, primaryTop string
	for i, w := range parseWorktrees(list) {
		path := igit.Canonical(w.path)
		dir := filepath.Join(path, rel)
		if i == 0 {
			search.remedy = dir
		}
		if samePath(path, top) || (i > 0 && w.head != mergeHead) {
			continue
		}
		if i == 0 {
			primaryDir, primaryTop = dir, path
			continue
		}
		search.elsewhere = append(search.elsewhere, dir)
		search.elsewhereTops = append(search.elsewhereTops, path)
	}
	if primaryDir != "" {
		search.elsewhere = append(search.elsewhere, primaryDir)
		search.elsewhereTops = append(search.elsewhereTops, primaryTop)
	}
	return search
}
