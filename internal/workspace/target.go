package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Target is a resolved worktree to act on. The git verbs (commit, push, pr,
// unclaim, ship) all operate on one Target, resolved either from the current
// directory (zero-arg — the common case for a coder session standing inside its
// worktree) or from an explicit <repo> <branch> pair (the same addressing
// prepare/claim/remove use, for driving a worktree from outside it).
type Target struct {
	Worktree string // absolute path to the working tree we run git in
	Branch   string // branch checked out there ("HEAD" when detached)
	MainRepo string // toplevel of the repo's MAIN (non-linked) working tree
	RepoName string // basename of MainRepo — what deriveService keys off
}

// ResolveTarget turns the optional positional <repo> <branch> override into a
// concrete worktree. Both empty => resolve the cwd's worktree (and its current
// branch). Both set => the prepared worktree at <repo-base>/<slug>, which must
// already exist (else it points at prepare). Exactly one set is a usage error:
// the two addressing modes don't mix.
func ResolveTarget(repoArg, branchArg, into string) (*Target, error) {
	switch {
	case repoArg == "" && branchArg == "":
		return resolveFromCwd()
	case repoArg != "" && branchArg != "":
		return resolveFromArgs(repoArg, branchArg, into)
	default:
		return nil, fmt.Errorf("provide both <repo> and <branch>, or neither (to act on the current worktree)")
	}
}

// resolveFromCwd resolves the worktree the caller is standing in.
func resolveFromCwd() (*Target, error) {
	top, err := gitToplevel(".")
	if err != nil {
		return nil, fmt.Errorf("not inside a git worktree (cd into one, or pass <repo> <branch>)")
	}
	main, err := mainWorktree(top)
	if err != nil {
		return nil, err
	}
	return &Target{
		Worktree: top,
		Branch:   currentBranch(top),
		MainRepo: main,
		RepoName: filepath.Base(main),
	}, nil
}

// resolveFromArgs resolves the prepared worktree for repo+branch, the same path
// prepare/claim/remove compute. The worktree must exist.
func resolveFromArgs(repoArg, branchArg, into string) (*Target, error) {
	slug, err := Slugify(branchArg)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(repoArg)
	if err != nil {
		return nil, err
	}
	top, err := gitToplevel(abs)
	if err != nil {
		return nil, err
	}
	base := into
	if base == "" {
		base = DefaultWorktreeBase(top)
	}
	wt := filepath.Join(base, slug)
	if !dirExists(wt) {
		return nil, fmt.Errorf("worktree not found: %s\n  run: aphrollo workspace prepare %s %s --apply", wt, repoArg, branchArg)
	}
	return &Target{
		Worktree: wt,
		Branch:   branchArg,
		MainRepo: top,
		RepoName: filepath.Base(top),
	}, nil
}

// mainWorktree returns the toplevel of the repo's primary (non-linked) working
// tree. `git worktree list --porcelain` always lists the main worktree first,
// so the first "worktree <path>" line is it — this resolves the canonical clone
// even when called from inside a linked worktree (what unclaim points back to).
func mainWorktree(path string) (string, error) {
	out, err := exec.Command("git", "-C", path, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("git worktree list: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("could not determine main worktree for %s", path)
}

// currentBranch returns the branch checked out at top, or "HEAD" when detached.
func currentBranch(top string) string {
	out, err := exec.Command("git", "-C", top, "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(out))
}
