package gitenv

import (
	"os"
	"path/filepath"
	"strings"
)

// CleanFor is Clean for a git call made in dir with the given arguments.
// `git commit -a` and `git commit <paths>` build a temporary index and run the
// pre-commit hook with GIT_INDEX_FILE naming it, while the default index is
// stale and locked for the length of the commit. A call the hook makes to read
// the staged tree of that repository must read the index the commit is
// writing, so it keeps GIT_INDEX_FILE, made absolute against the hook's
// working directory.
//
// Every other call gets none: a call for another repository, or for a linked
// worktree of this one, has an index of its own, and a call that writes an
// index or checks a tree out would write through the commit's index instead
// (`git worktree add` resets it to HEAD, and the commit lands empty). Only the
// subcommands in stagedTreeReaders keep it, so a call this package has not
// named as a read never inherits it.
func CleanFor(dir string, args ...string) []string {
	env := Clean()
	if !readsStagedTree(args) {
		return env
	}
	if idx := hookIndexFor(dir); idx != "" {
		env = append(env, "GIT_INDEX_FILE="+idx)
	}
	return env
}

// stagedTreeReaders are the subcommands that only read the index, or write
// objects and never an index.
var stagedTreeReaders = map[string]bool{
	"ls-files":   true,
	"diff":       true,
	"diff-index": true,
	"show":       true,
	"cat-file":   true,
	"write-tree": true,
}

// globalOptionsWithValue are the git options before the subcommand that take
// their value as the next argument.
var globalOptionsWithValue = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true}

// readsStagedTree reports whether args run one of stagedTreeReaders. It skips
// the options git takes before the subcommand; no subcommand is no read.
func readsStagedTree(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case globalOptionsWithValue[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return stagedTreeReaders[a]
		}
	}
	return false
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

// HookIndex is the index a staged-tree read in dir uses: the process's
// GIT_INDEX_FILE, absolute against the working directory, when it lies in the
// git directory of the repository dir is in, and "" otherwise (the repository's
// own index). It is the rule CleanFor applies to the child it spawns.
func HookIndex(dir string) string { return hookIndexFor(dir) }
