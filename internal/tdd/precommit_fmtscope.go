package tdd

import (
	"path/filepath"
	"sort"
	"strings"
)

// laneGoFiles is the file set the format check judges: the files this commit
// stages, plus every OTHER file the lane has already changed against its base.
//
// The two gates disagreed on scope. Pre-commit judged the staged set; the
// merge gate judges the lane's whole diff. A file an earlier commit in the
// lane left unformatted was therefore invisible to every later commit's gate
// and surfaced only at the merge (escape #216, `gofmt → REJECTED
// internal/workspace/pr_test.go` after a pre-commit gate that ran green on
// the same tree). A guard's scan scope must equal its rule's scope, and the
// rule is that the lane merges formatted.
//
// Only the format check widens. It is a parse rather than a process spawn, so
// the extra files cost nothing measurable, while lint and the suites stay
// scoped to the commit for exactly the opposite reason.
//
// Files the lane never touched are NOT included: blocking a lane for a
// violation that was already on its base makes the gate unpassable and
// teaches people to bypass it, and the merge gate does not judge those
// either.
func laneGoFiles(repoRoot, root string, touched []string) []string {
	seen := make(map[string]bool, len(touched))
	out := make([]string, 0, len(touched))
	for _, rel := range touched {
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}

	var extra []string
	for _, repoRel := range laneChangedPaths(repoRoot) {
		if !strings.HasSuffix(repoRel, ".go") {
			continue
		}
		rel, err := filepath.Rel(root, filepath.Join(repoRoot, filepath.FromSlash(repoRel)))
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // outside this Go root: another root's gate judges it
		}
		if !seen[rel] {
			seen[rel] = true
			extra = append(extra, rel)
		}
	}
	// Sorted so the rejection message is stable whatever order git listed
	// them in; the staged files keep their own order ahead of these.
	sort.Strings(extra)
	return append(out, extra...)
}
