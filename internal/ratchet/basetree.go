package ratchet

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
	// ReadAll answers the same content as calling Read once per path, but in
	// one round trip: a diff-scoped law over a large in-scope set spawns one
	// process instead of one per file. A path absent at base is simply absent
	// from the result — not an error.
	ReadAll(paths []string) (map[string][]byte, error)
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

// ReadAll reads every path through one `git cat-file --batch` process fed
// `<ref>:<path>` per line on stdin — a diff-scoped law over hundreds of
// in-scope files spawns one process, not one per file. Output order matches
// input order (git's own guarantee), so each header is read against the
// path that produced it. A path git reports "missing" (absent at ref) is
// left out of the result rather than treated as a read failure — the same
// tolerance List()+Read() already had for a path that vanished mid-walk.
func (g *gitBaseReader) ReadAll(paths []string) (map[string][]byte, error) {
	if len(paths) == 0 {
		return map[string][]byte{}, nil
	}
	var stdin strings.Builder
	for _, p := range paths {
		stdin.WriteString(g.ref + ":" + p + "\n")
	}
	cmd := exec.Command("git", "-C", g.root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(stdin.String())
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w", err)
	}
	return ParseCatFileBatch(out, paths)
}

// ParseCatFileBatch reads one `git cat-file --batch` invocation's stdout,
// for the SAME paths (in the SAME order) that were fed to it on stdin --
// git's own ordering guarantee is what lets each header be read against the
// path that produced it. A path git reports "missing" (absent at whatever
// ref it was asked for) is left out of the result rather than treated as a
// read failure, matching the tolerance a single `git show` already has for
// a path absent at a ref.
//
// Exported so a caller with its own reasons to build the `exec.Command`
// itself — the staged-baseline guard runs every git subprocess through a
// scrubbed environment, because a nested git call inheriting a hook's own
// GIT_DIR/GIT_INDEX_FILE would operate on the wrong repo state — reuses the
// batch protocol's parsing without also reusing gitBaseReader's own,
// unscrubbed exec.Command (#489).
func ParseCatFileBatch(out []byte, paths []string) (map[string][]byte, error) {
	result := map[string][]byte{}
	r := bufio.NewReader(bytes.NewReader(out))
	for _, p := range paths {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: reading header for %s: %w", p, err)
		}
		header = strings.TrimSuffix(header, "\n")
		if strings.HasSuffix(header, " missing") {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git cat-file --batch: malformed header %q for %s", header, p)
		}
		size, convErr := strconv.Atoi(fields[2])
		if convErr != nil {
			return nil, fmt.Errorf("git cat-file --batch: bad size in %q for %s: %w", header, p, convErr)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("git cat-file --batch: reading %d bytes for %s: %w", size, p, err)
		}
		if _, err := r.ReadByte(); err != nil { // trailing LF after the object body
			return nil, fmt.Errorf("git cat-file --batch: reading trailing newline for %s: %w", p, err)
		}
		result[p] = data
	}
	return result, nil
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

// ReadAll reads each path off disk; one absent from the fixture's tree is
// left out of the result, matching gitBaseReader's "missing" tolerance.
func (d dirBaseReader) ReadAll(paths []string) (map[string][]byte, error) {
	result := map[string][]byte{}
	for _, p := range paths {
		data, err := d.Read(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		result[p] = data
	}
	return result, nil
}

// baseRefNotFound reports whether err came from git failing to resolve the
// base REF itself (an unborn branch, a typo'd ref) rather than some other
// failure (a missing git binary, a permission error) that must still stop
// the run. `git ls-tree`/`show` say "not found" in one of two ways: the
// literal message, or a bare exit 128 with nothing more specific — both are
// covered here so a fresh repo's unborn HEAD reads the same as an unknown
// revision. A missing git binary surfaces as *exec.Error, which errors.As
// does not match here, so it is never swallowed as "not found".
func baseRefNotFound(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	stderr := string(exitErr.Stderr)
	if strings.Contains(stderr, "Not a valid object name") || strings.Contains(stderr, "unknown revision") {
		return true
	}
	return exitErr.ExitCode() == 128
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

// resolveLaneBaseTree is resolveBaseTree's twin for `[scope] changed = "lane"`:
// whatever Options supplied outright, else a git-backed reader over LaneBase —
// nil when neither is set, the same "no base" tolerance every diff-scoped kind
// already has.
func resolveLaneBaseTree(opts Options) BaseReader {
	if opts.LaneBaseTree != nil {
		return opts.LaneBaseTree
	}
	if opts.LaneBase == "" {
		return nil
	}
	return &gitBaseReader{root: opts.Root, ref: opts.LaneBase}
}
