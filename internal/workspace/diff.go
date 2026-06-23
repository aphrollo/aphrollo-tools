package workspace

import (
	"fmt"
	"io"
	"os/exec"
)

// Diff prints the target branch's PR diff: `git diff <base>...HEAD`, where base
// is the repo's default remote branch (origin/<default>, resolved the same way
// the rest of the package reads it — never hardcoded "main"). The three-dot form
// diffs HEAD against the MERGE-BASE of base and HEAD, so it shows exactly the
// branch's own changes, not unrelated commits base gained since the fork.
//
// It is read-only (no --dry) and lossless: git's diff bytes are streamed
// verbatim. --stat renders the diffstat summary instead of the full patch. An
// empty diff is success (exit 0), printing nothing.
func Diff(t *Target, stat bool, stdout, stderr io.Writer) error {
	base := diffBase(t.Worktree)
	args := []string{"-C", t.Worktree, "diff"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, base+"...HEAD")
	cmd := exec.Command("git", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git diff %s...HEAD: %w", base, err)
	}
	return nil
}

// diffBase resolves the ref the PR diff is taken against: the repo's default
// remote branch on origin (origin/<default>) when it exists, else the local
// default branch — so an offline clone with no origin ref still diffs against
// the right base instead of failing.
func diffBase(wt string) string {
	def := resolveDefaultBranch(wt)
	if remote := "origin/" + def; gitRefExists(wt, remote) {
		return remote
	}
	return def
}
