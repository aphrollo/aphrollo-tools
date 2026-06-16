package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// Prune clears the admin records of worktrees whose directories are gone —
// `git worktree prune`. git's own --dry-run does the previewing, so Prune maps
// cleanly onto the package's dry-run/--apply contract: the dry-run shows what
// would be removed, --apply removes it and reports the count.
type Prune struct {
	Repo string // repo toplevel to prune
}

// PrunePlan resolves the repo (cwd when repoArg is "") without executing.
func PrunePlan(repoArg string) (*Prune, error) {
	path := repoArg
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	top, err := gitToplevel(abs)
	if err != nil {
		return nil, err
	}
	return &Prune{Repo: top}, nil
}

// Run executes the prune. apply=false runs `git worktree prune --dry-run -v`
// (read-only); apply=true runs the real prune. Either way it reports the stale
// entries by path, or that there were none.
func (p *Prune) Run(apply bool, stdout, stderr io.Writer) error {
	args := []string{"-C", p.Repo, "worktree", "prune", "-v"}
	if !apply {
		args = append(args, "--dry-run")
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		fmt.Fprint(stderr, string(out))
		return fmt.Errorf("git worktree prune: %w", err)
	}
	paths := prunedPaths(string(out))
	verb := "would prune"
	if apply {
		verb = "pruned"
	}
	if len(paths) == 0 {
		fmt.Fprintf(stdout, "no stale worktrees to prune\n")
		return nil
	}
	fmt.Fprintf(stdout, "%s %d stale worktree(s):\n", verb, len(paths))
	for _, pth := range paths {
		fmt.Fprintf(stdout, "  %s\n", pth)
	}
	return nil
}

// prunedPaths pulls the worktree paths out of `git worktree prune -v` output.
// git prints lines like: "Removing worktrees/<name>: gitdir file points to
// non-existent location" — the path is between "Removing " and the colon.
func prunedPaths(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, "Removing ")
		if !ok {
			continue
		}
		if i := strings.Index(rest, ":"); i >= 0 {
			rest = rest[:i]
		}
		paths = append(paths, strings.TrimSpace(rest))
	}
	return paths
}
