package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Cleanup tears down a finished worktree: `git worktree remove` followed by
// `git worktree prune`, folded into one command for the post-merge step. It
// refuses to remove the worktree the caller is standing in — that would yank the
// session's own cwd out from under it — and points at the main clone instead.
type Cleanup struct {
	Repo     string // main clone toplevel
	Branch   string
	Worktree string
	Force    bool // pass --force to remove a worktree with untracked/modified files
}

// CleanupPlan resolves the worktree for branch under repoArg (or, when repoArg
// is "", the main clone of the cwd) without executing. It errors when the
// worktree is missing or when the cwd is inside it.
func CleanupPlan(repoArg, branch, into string, force bool) (*Cleanup, error) {
	slug, err := Slugify(branch)
	if err != nil {
		return nil, err
	}
	top, err := resolveMainClone(repoArg)
	if err != nil {
		return nil, err
	}
	base := into
	if base == "" {
		base = DefaultWorktreeBase(top)
	}
	wt := filepath.Join(base, slug)
	if !dirExists(wt) {
		return nil, fmt.Errorf("worktree not found: %s", wt)
	}
	if cwd, err := os.Getwd(); err == nil && pathWithin(cwd, wt) {
		return nil, fmt.Errorf("refusing to remove the worktree you're standing in — cd out first:\n  cd %s && aphrollo workspace cleanup %s --apply", top, branch)
	}
	return &Cleanup{Repo: top, Branch: branch, Worktree: wt, Force: force}, nil
}

// Run removes the worktree and prunes. apply=false previews; apply=true executes
// and reports. The prune sweeps any other stale records too, so cleanup leaves
// the repo's worktree list tidy, not just this branch removed.
func (c *Cleanup) Run(apply bool, stdout, stderr io.Writer) error {
	rmArgs := []string{"-C", c.Repo, "worktree", "remove"}
	if c.Force {
		rmArgs = append(rmArgs, "--force")
	}
	rmArgs = append(rmArgs, c.Worktree)

	if !apply {
		fmt.Fprintf(stdout, "workspace cleanup: %s @ %s\n", filepath.Base(c.Repo), c.Branch)
		fmt.Fprintf(stdout, "  worktree: %s\n", c.Worktree)
		fmt.Fprintf(stdout, "\nsteps (dry-run — pass --apply to execute):\n")
		fmt.Fprintf(stdout, "  1. [run] %s\n", shellJoin(rmArgs))
		fmt.Fprintf(stdout, "  2. [run] git -C %s worktree prune -v\n", c.Repo)
		fmt.Fprintf(stdout, "\nrun again with --apply to remove the worktree and prune.\n")
		return nil
	}

	combined, err := exec.Command("git", rmArgs...).CombinedOutput()
	if err != nil {
		fmt.Fprint(stderr, string(combined))
		return fmt.Errorf("git worktree remove: %w (use --force if the tree has local changes)", err)
	}
	fmt.Fprintf(stdout, "removed worktree %s\n", c.Worktree)
	// The worktree's files (and its node_modules) are gone now, so any LSP or
	// editor diagnostics still pointing under that path are phantom — unresolved
	// imports against a directory that no longer exists. Say so explicitly,
	// naming the path, so a running session does not act on the stale diagnostics
	// that surface right after removal. The merged code is unaffected.
	fmt.Fprintf(stdout, "note: any LSP/editor diagnostics under %s are now stale (its files are gone) — ignore them; they clear on their own.\n", c.Worktree)

	// Reuse the prune verb so any other stale records are swept in the same call.
	return (&Prune{Repo: c.Repo}).Run(true, stdout, stderr)
}

// resolveMainClone returns the main-clone toplevel for repoArg, or — when
// repoArg is "" — the main clone of the cwd's repo (so `cleanup <branch>` works
// from anywhere in the tree).
func resolveMainClone(repoArg string) (string, error) {
	if repoArg == "" {
		top, err := gitToplevel(".")
		if err != nil {
			return "", fmt.Errorf("not inside a git repository (cd into the repo, or pass <repo>)")
		}
		return mainWorktree(top)
	}
	abs, err := filepath.Abs(repoArg)
	if err != nil {
		return "", err
	}
	top, err := gitToplevel(abs)
	if err != nil {
		return "", err
	}
	return mainWorktree(top)
}

// pathWithin reports whether child is parent or a descendant of it, comparing
// cleaned absolute paths.
func pathWithin(child, parent string) bool {
	c, err1 := filepath.Abs(child)
	p, err2 := filepath.Abs(parent)
	if err1 != nil || err2 != nil {
		return false
	}
	c, p = filepath.Clean(c), filepath.Clean(p)
	return c == p || strings.HasPrefix(c, p+string(filepath.Separator))
}
