package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// CommitStagedSubset commits the STAGED content of paths, alone, on top of
// HEAD and moves the current branch to it — without touching the working
// tree or the real index. The rest of the staged change is still staged
// afterwards, now measured against the new HEAD, so an ordinary `git commit`
// takes it as the second commit.
//
// It works on a scratch index: HEAD's tree is read into it, each path's
// staged entry (or its staged deletion) is copied over from the real index,
// and that tree becomes the commit. Nothing is checked out, restored, reset
// or stashed, so an unstaged edit or an untracked file cannot be lost. The
// commit is written with plumbing (write-tree, commit-tree, update-ref), which
// runs no commit hook: the caller is the one that already proved the subset
// safe to commit alone.
//
// Every refusal happens before update-ref, so a failed call leaves HEAD and
// the index exactly as they were. Returns the new commit's hash.
func CommitStagedSubset(repoRoot string, paths []string, msg string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("no paths to commit")
	}
	if strings.TrimSpace(msg) == "" {
		return "", errors.New("a commit message is required")
	}
	head, err := git(repoRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("no commit to build on (HEAD does not resolve): %s", strings.TrimSpace(head))
	}
	head = strings.TrimSpace(head)
	gitDir, err := git(repoRoot, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("cannot locate the git dir: %s", strings.TrimSpace(gitDir))
	}
	scratch := filepath.Join(strings.TrimSpace(gitDir), fmt.Sprintf("aphrollo-split-index-%d", os.Getpid()))
	defer os.Remove(scratch)

	if out, err := gitIndexed(repoRoot, scratch, "read-tree", head); err != nil {
		return "", fmt.Errorf("reading HEAD into the scratch index: %s", strings.TrimSpace(out))
	}
	for _, p := range paths {
		if err := copyStagedEntryInto(repoRoot, scratch, head, p); err != nil {
			return "", err
		}
	}
	tree, err := gitIndexed(repoRoot, scratch, "write-tree")
	if err != nil {
		return "", fmt.Errorf("writing the tree: %s", strings.TrimSpace(tree))
	}
	tree = strings.TrimSpace(tree)
	headTree, err := git(repoRoot, "rev-parse", "--verify", head+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("reading HEAD's tree: %s", strings.TrimSpace(headTree))
	}
	if tree == strings.TrimSpace(headTree) {
		return "", fmt.Errorf("the staged content of %s is identical to HEAD, so there is nothing to commit", strings.Join(paths, ", "))
	}
	commit, err := git(repoRoot, "commit-tree", tree, "-p", head, "-m", msg)
	if err != nil {
		return "", fmt.Errorf("writing the commit: %s", strings.TrimSpace(commit))
	}
	commit = strings.TrimSpace(commit)
	if out, err := git(repoRoot, "update-ref", "-m", "split-commit: "+splitSubject(msg), "HEAD", commit, head); err != nil {
		return "", fmt.Errorf("moving the branch (HEAD changed underneath the split?): %s", strings.TrimSpace(out))
	}
	return commit, nil
}

// copyStagedEntryInto makes the scratch index carry path exactly as the real
// index stages it: its blob and mode, or its absence when the change is a
// staged deletion. A path that is neither staged nor in HEAD names nothing
// and is refused, so a typo or a glob cannot pass as an empty subset.
func copyStagedEntryInto(repoRoot, scratch, head, path string) error {
	out, err := git(repoRoot, "--literal-pathspecs", "ls-files", "--stage", "-z", "--", path)
	if err != nil {
		return fmt.Errorf("reading the staged entry of %s: %s", path, strings.TrimSpace(out))
	}
	entries := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if out == "" {
		if _, err := git(repoRoot, "cat-file", "-e", head+":"+path); err != nil {
			return fmt.Errorf("%s is neither staged nor in HEAD", path)
		}
		if out, err := gitIndexed(repoRoot, scratch, "--literal-pathspecs", "update-index", "--force-remove", "--", path); err != nil {
			return fmt.Errorf("removing %s from the scratch index: %s", path, strings.TrimSpace(out))
		}
		return nil
	}
	if len(entries) != 1 {
		return fmt.Errorf("%s has %d index entries (an unresolved merge?); resolve it before splitting", path, len(entries))
	}
	// "<mode> <sha> <stage>\t<path>"
	meta, _, _ := strings.Cut(entries[0], "\t")
	fields := strings.Fields(meta)
	if len(fields) != 3 || fields[2] != "0" {
		return fmt.Errorf("%s is not cleanly staged (index entry %q)", path, entries[0])
	}
	cacheinfo := fields[0] + "," + fields[1] + "," + path
	if out, err := gitIndexed(repoRoot, scratch, "update-index", "--add", "--cacheinfo", cacheinfo); err != nil {
		return fmt.Errorf("copying %s into the scratch index: %s", path, strings.TrimSpace(out))
	}
	return nil
}

// gitIndexed runs git in dir against the scratch index file instead of the
// repo's own, and returns STDOUT (STDERR on failure, as git does).
func gitIndexed(dir, index string, args ...string) (string, error) {
	out, err := outputGit(run.Spec{Name: gitBinary(), Args: args, Dir: dir, Env: append(cleanGitEnv(), "GIT_INDEX_FILE="+index)})
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(ee.Stderr), err
		}
		return "", err
	}
	return string(out), nil
}

// splitSubject is the subject of a commit message.
func splitSubject(msg string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	return line
}
