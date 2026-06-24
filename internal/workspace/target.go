package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Target is a resolved worktree to act on. The git verbs (commit, push, pr,
// unclaim, ship) all operate on one Target, resolved either from the current
// directory (zero-arg — the common case for a coder session standing inside its
// worktree) or from an explicit <repo> <branch> pair (the same addressing
// create/claim/remove use, for driving a worktree from outside it).
type Target struct {
	Worktree string // absolute path to the working tree we run git in
	Branch   string // branch checked out there ("HEAD" when detached)
	MainRepo string // toplevel of the repo's MAIN (non-linked) working tree
	RepoName string // basename of MainRepo — what deriveService keys off
}

// ResolveTarget turns the optional positional <repo> <branch> override into a
// concrete worktree. Both empty => resolve the cwd's worktree (and its current
// branch). Both set => the prepared worktree at <repo-base>/<slug>, which must
// already exist (else it points at create). Exactly one set is a usage error:
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
// create/claim/remove compute. The worktree must exist.
func resolveFromArgs(repoArg, branchArg, into string) (*Target, error) {
	slug, err := Slugify(branchArg)
	if err != nil {
		return nil, err
	}
	top, err := resolveMainRepo(repoArg)
	if err != nil {
		return nil, err
	}
	base := into
	if base == "" {
		base = DefaultWorktreeBase(top)
	}
	wt := filepath.Join(base, slug)
	if !dirExists(wt) {
		return nil, fmt.Errorf("worktree not found: %s\n  run: aphrollo workspace create %s %s", wt, repoArg, branchArg)
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

// resolveMainRepo turns a <repo> POSITIONAL into the toplevel of the repo's
// main (non-linked) working tree, tolerant of where the caller stands. Every
// arg-addressed verb (prepare + the git verbs) routes its <repo> through here so
// a bare name like "aphrollo-web" resolves the SAME from inside the clone, from
// one of its worktrees, or from a sibling clone — never double-joining the name
// against cwd via filepath.Abs (the bug this centralizes away).
//
// Resolution order:
//  1. A path (absolute, or relative to cwd) that is itself a git repo wins as
//     its toplevel — genuine path args keep working unchanged. A bare name run
//     from inside its own clone fails here (cwd/<name> is not a repo) and falls
//     through, which is exactly the double-join case the old code mishandled.
//  2. A bare NAME matching the clone the caller stands in (or whose linked
//     worktree they stand in) — the natural "run it from inside the repo".
//  3. A bare NAME with exactly one git-repo match under the spaces tree
//     (<spacesParent>/*/<repo>). More than one is an ambiguity error listing the
//     candidates; the caller disambiguates with an absolute path.
//  4. Otherwise an actionable error naming what was tried and the fixes.
func resolveMainRepo(repoArg string) (string, error) {
	if repoArg == "" {
		return "", fmt.Errorf("repo path required")
	}
	// 1. A real path (abs or cwd-relative) that resolves to a git repo.
	if abs, err := filepath.Abs(repoArg); err == nil {
		if top, err := gitToplevel(abs); err == nil {
			return top, nil
		}
	}
	// Only a bare name (no path separator) gets the by-name treatment; a
	// separator-bearing arg that failed (1) is a genuine bad path.
	if !strings.ContainsRune(repoArg, filepath.Separator) {
		// 2. The clone the caller stands in (mainWorktree resolves a linked
		//    worktree back to its main clone, so both cases are covered).
		if cwdTop, err := gitToplevel("."); err == nil {
			if main, err := mainWorktree(cwdTop); err == nil && filepath.Base(main) == repoArg {
				return main, nil
			}
		}
		// 3. A unique match under the spaces tree.
		matches, err := spacesClones(repoArg)
		if err != nil {
			return "", err
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
	}
	// 4. Unresolvable — name what was tried and how to fix it.
	return "", fmt.Errorf("could not resolve repo %q: not a git repo at that path, "+
		"not the clone you're standing in, and no unique match under %s\n"+
		"  fix: run from the worktree with no args, pass an absolute path, "+
		"or run from the spaces parent", repoArg, spacesParent())
}

// spacesParent is the dir holding the per-owner spaces dirs (~/spaces). A bare
// <repo> name resolves by globbing <spacesParent>/*/<repo> for a git repo of
// that name. Overridable with APHROLLO_SPACES_ROOT (tests, non-standard homes).
func spacesParent() string {
	if d := os.Getenv("APHROLLO_SPACES_ROOT"); d != "" {
		return d
	}
	return "/home/debian/spaces"
}

// spacesClones returns every git-repo toplevel named repo under the spaces tree
// (<spacesParent>/*/<repo>), sorted and de-duplicated. More than one is an
// ambiguity error that LISTS the candidates rather than silently picking one —
// the caller disambiguates with an absolute path. Zero matches returns
// (nil, nil) so resolveMainRepo can fall through to its actionable not-found.
func spacesClones(repo string) ([]string, error) {
	hits, _ := filepath.Glob(filepath.Join(spacesParent(), "*", repo))
	var tops []string
	seen := map[string]bool{}
	for _, h := range hits {
		top, err := gitToplevel(h)
		// Keep only true clone roots: the hit's own toplevel must still carry the
		// name (rejects a dir merely TRACKED by a parent repo, whose toplevel is
		// the differently-named ancestor). Comparing the basename — not top == h —
		// stays correct when the spaces root is a symlink (gitToplevel canonicalizes).
		if err != nil || filepath.Base(top) != repo || seen[top] {
			continue
		}
		seen[top] = true
		tops = append(tops, top)
	}
	sort.Strings(tops)
	if len(tops) > 1 {
		return nil, fmt.Errorf("ambiguous repo name %q — matches %d clones under %s:\n  %s\n  pass an absolute path to pick one",
			repo, len(tops), spacesParent(), strings.Join(tops, "\n  "))
	}
	return tops, nil
}

// currentBranch returns the branch checked out at top, or "HEAD" when detached.
func currentBranch(top string) string {
	out, err := exec.Command("git", "-C", top, "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(out))
}
