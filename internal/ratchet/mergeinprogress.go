package ratchet

import (
	"os"
	"path/filepath"
	"strings"
)

// mergeHeadPresent reports whether root (a repo or worktree checkout) is
// currently mid-merge -- git's own MERGE_HEAD marker, present for the exact
// window a rejected pre-merge-commit hook leaves behind (index and worktree
// already carry the merge result, no commit yet) and gone again once the
// merge concludes, by commit or by abort. A tightening run that scans DURING
// that window reads files that are not yet at the paths the just-merged
// INDEX already carries (a rename mid-flight, observed pre-move on disk),
// concludes those keys are gone, and rewrites the baseline without them --
// deleting rows the index correctly carries, for a state that never existed
// as a commit. The fix is refusing to scan there at all, not scanning more
// carefully: the tree mid-merge is not a tree any commit will ever equal.
func mergeHeadPresent(root string) bool {
	return isFile(filepath.Join(gitDirFor(root), "MERGE_HEAD"))
}

// tightenBaseline applies one law's tightening -- extracted from Check so
// the merge-in-progress refusal lives beside the marker it tests. Every
// existing condition still applies (an actual Tighten run, a real baseline
// path, a whole, non-hypothetical scan); the fix this adds is refusing when
// root is mid-merge, the same way a hypothetical or narrowed scan already
// refuses -- a tree mid-merge is not a tree any commit will ever equal, so a
// baseline it writes is derived from a state that never existed as one. It
// returns the law's baseline name when it actually wrote a changed file, ""
// otherwise, so the caller only appends when something really moved.
func tightenBaseline(opts Options, law Law, baseline *Baseline, path string, measured map[string]int, sites map[string][]string) (string, error) {
	if !opts.Tighten || path == "" || len(opts.Proposed) > 0 || len(opts.Files) > 0 {
		return "", nil
	}
	if mergeHeadPresent(opts.Root) {
		return "", nil
	}
	// Tighten unconditionally and let WriteIfChanged decide: a count that
	// did not move can still leave a row naming a file that is gone, and
	// re-pathing it is the whole point of a path-agnostic key. The write is
	// byte-stable, so a tree with nothing to fix still writes nothing.
	baseline.TightenWithSites(measured, sites)
	wrote, err := baseline.WriteIfChanged(path)
	if err != nil {
		return "", err
	}
	if !wrote {
		return "", nil
	}
	return law.Baseline, nil
}

// gitDirFor resolves root's git directory, following a WORKTREE's ".git"
// file redirect the way git itself does: a linked worktree's ".git" is a
// one-line file ("gitdir: <path>/.git/worktrees/<name>"), not a directory,
// and MERGE_HEAD for that worktree lives beside the redirect target, never
// beside root/.git. Any failure to read or parse it falls back to the plain
// root/.git guess -- mergeHeadPresent then simply finds no MERGE_HEAD there,
// which is the same "not mid-merge" answer a missing repo would give.
func gitDirFor(root string) string {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Stat(dotGit)
	if err != nil || info.IsDir() {
		return dotGit
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit
	}
	const prefix = "gitdir:"
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, prefix) {
		return dotGit
	}
	target := strings.TrimSpace(line[len(prefix):])
	if target == "" {
		return dotGit
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	return target
}
