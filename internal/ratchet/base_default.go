package ratchet

import "strings"

// defaultBranchRefs are tried, in order, when origin names no default branch
// of its own (a clone that never set origin/HEAD).
var defaultBranchRefs = []string{"origin/main", "origin/master"}

// DefaultBase is the ref a `ratchet check` run with no --base is judged
// against: the merge base of HEAD and origin's default branch, which is where
// a lane left trunk. sha is that commit and ref the origin branch it came
// from; ok is false with no origin, no default branch to resolve, or no
// common history, and the caller then keeps its "no base" skip.
func DefaultBase(root string) (sha, ref string, ok bool) {
	g := &gitBaseReader{root: root}
	candidates := defaultBranchRefs
	if out, err := gitOutput(g.git("symbolic-ref", "--short", "refs/remotes/origin/HEAD")); err == nil {
		if r := strings.TrimSpace(string(out)); r != "" {
			candidates = append([]string{r}, candidates...)
		}
	}
	for _, c := range candidates {
		if _, err := gitOutput(g.git("rev-parse", "--verify", "--quiet", c+"^{commit}")); err != nil {
			continue
		}
		out, err := gitOutput(g.git("merge-base", "HEAD", c))
		if err != nil {
			continue
		}
		if sha = strings.TrimSpace(string(out)); sha != "" {
			return sha, c, true
		}
	}
	return "", "", false
}
