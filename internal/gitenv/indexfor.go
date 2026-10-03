package gitenv

import (
	"os"
	"path/filepath"
	"strings"
)

// CleanFor is Clean for a git call made in dir. `git commit -a` and
// `git commit <paths>` build a temporary index and run the pre-commit hook
// with GIT_INDEX_FILE naming it, while the default index is stale and locked
// for the length of the commit. A call the hook makes for that repository
// must read the index the commit is writing, so it keeps GIT_INDEX_FILE, made
// absolute against the hook's working directory; a call for any other
// repository, or for a linked worktree of it, whose index is its own, gets
// none.
func CleanFor(dir string) []string {
	env := Clean()
	if idx := hookIndexFor(dir); idx != "" {
		env = append(env, "GIT_INDEX_FILE="+idx)
	}
	return env
}

// hookIndexFor answers the process's GIT_INDEX_FILE, absolute, when it lies
// in the git directory of the repository dir is in, and "" otherwise.
func hookIndexFor(dir string) string {
	idx := os.Getenv("GIT_INDEX_FILE")
	if idx == "" {
		return ""
	}
	idx, err := filepath.Abs(idx)
	if err != nil {
		return ""
	}
	gitDir := gitDirOf(dir)
	if gitDir == "" {
		return ""
	}
	want, err := os.Stat(gitDir)
	if err != nil {
		return ""
	}
	have, err := os.Stat(filepath.Dir(idx))
	if err != nil || !os.SameFile(want, have) {
		return ""
	}
	return idx
}

// gitDirOf finds the git directory of the repository at or above dir: the
// `.git` directory itself, or the one a linked worktree's `.git` file names.
// "" when dir is in no repository.
func gitDirOf(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	// walk-terminates: dir becomes its parent each turn, and the walk returns at the root
	for {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			if info.IsDir() {
				return dotGit
			}
			// An unreadable file reads as empty text, which names no git directory.
			text, _ := os.ReadFile(dotGit)
			target, ok := strings.CutPrefix(strings.TrimSpace(string(text)), "gitdir:")
			if !ok {
				return ""
			}
			target = strings.TrimSpace(target)
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return target
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
