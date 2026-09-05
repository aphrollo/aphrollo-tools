package ratchet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// BaseReader answers a diff-scoped law's OTHER tree: every path present, and
// one path's content, at whatever "base" means for this run — a git ref for
// a real repo, a checked-in directory for a fixture. It is how symbol-removed
// (and any diff-scoped kind after it) reads both sides of a diff through the
// same interface, real or fixture.
type BaseReader interface {
	List() ([]string, error)
	Read(path string) ([]byte, error)
}

// gitBaseReader answers from a git ref, via `git -C root ls-tree`/`show` —
// the COMMITTED tree, never the worktree, so a dirty file never lies about
// what a diff is actually against.
type gitBaseReader struct {
	root string
	ref  string
}

func (g *gitBaseReader) List() ([]string, error) {
	out, err := exec.Command("git", "-C", g.root, "ls-tree", "-r", "--name-only", g.ref).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree -r --name-only %s: %w", g.ref, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (g *gitBaseReader) Read(path string) ([]byte, error) {
	out, err := exec.Command("git", "-C", g.root, "show", g.ref+":"+path).Output()
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", g.ref, path, err)
	}
	return out, nil
}

// dirBaseReader answers from a plain directory — a fixture's checked-in
// `base/` tree standing in for a git ref, so a law's fixtures never need a
// real git history to prove the diff-scoped side of the rule.
type dirBaseReader struct {
	dir string
}

func (d dirBaseReader) List() ([]string, error) {
	var out []string
	err := filepath.WalkDir(d.dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(d.dir, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (d dirBaseReader) Read(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.dir, filepath.FromSlash(path)))
}

// resolveBaseTree is the BaseReader a diff-scoped law reads from: whatever
// Options supplied outright (a fixture's directory-backed reader), else a
// git-backed one over Base — nil when neither is set, which is "no base".
func resolveBaseTree(opts Options) BaseReader {
	if opts.BaseTree != nil {
		return opts.BaseTree
	}
	if opts.Base == "" {
		return nil
	}
	return &gitBaseReader{root: opts.Root, ref: opts.Base}
}
