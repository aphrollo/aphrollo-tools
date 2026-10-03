package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Head is where a worktree's HEAD points. Ref is the full ref of the branch it
// is on, Branch that ref without `refs/heads/`, and both are "" on a detached
// HEAD. SHA is the commit, "" on a branch with no commit yet.
type Head struct {
	Ref, Branch, SHA string
	Detached         bool
}

// Worktree is one worktree of the repository: its top directory and its HEAD.
type Worktree struct {
	Path string
	Head Head
}

// maxSymref bounds how many symbolic refs a lookup follows, as git does.
const maxSymref = 5

// isObjectID reports a full SHA-1 or SHA-256 object name.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Head reads this worktree's HEAD from its files. In a repository whose refs
// are in the reftable format there is no file to read, and git is asked.
func (c *Client) Head() (Head, error) { return c.headOf(c.gitDir, c.root) }

// headOf reads the HEAD in gitDir, the git directory of the worktree at dir.
func (c *Client) headOf(gitDir, dir string) (Head, error) {
	if c.reftable {
		return c.headFromGit(dir)
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return Head{}, err
	}
	text := strings.TrimSpace(string(raw))
	if ref, ok := strings.CutPrefix(text, "ref: "); ok {
		return Head{Ref: ref, Branch: strings.TrimPrefix(ref, "refs/heads/"), SHA: c.readRef(ref)}, nil
	}
	if !isObjectID(text) {
		return Head{}, fmt.Errorf("git: %s holds neither a ref nor a commit: %q", filepath.Join(gitDir, "HEAD"), text)
	}
	return Head{SHA: text, Detached: true}, nil
}

// headFromGit is headOf for a reftable repository: git names the ref (it
// fails on a detached HEAD) and the commit (it fails on a branch with none).
func (c *Client) headFromGit(dir string) (Head, error) {
	var h Head
	if out, err := c.outputIn(dir, "symbolic-ref", "-q", "HEAD"); err == nil {
		h.Ref = strings.TrimSpace(out)
		h.Branch = strings.TrimPrefix(h.Ref, "refs/heads/")
	} else {
		h.Detached = true
	}
	if out, err := c.outputIn(dir, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
		h.SHA = strings.TrimSpace(out)
	}
	return h, nil
}

// readRef is the commit ref names, from its loose file or from packed-refs,
// following symbolic refs; "" when it names none.
func (c *Client) readRef(ref string) string {
	for range maxSymref {
		raw, err := os.ReadFile(filepath.Join(c.commonDir, filepath.FromSlash(ref)))
		if err != nil {
			return c.packedRef(ref)
		}
		text := strings.TrimSpace(string(raw))
		target, symbolic := strings.CutPrefix(text, "ref: ")
		if !symbolic {
			if isObjectID(text) {
				return text
			}
			return ""
		}
		ref = target
	}
	return ""
}

// packedRef looks ref up in packed-refs, whose lines are `<sha> <ref>`, with
// `#` header lines and `^` lines for the peeled tag above.
func (c *Client) packedRef(ref string) string {
	raw, err := os.ReadFile(filepath.Join(c.commonDir, "packed-refs"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		sha, name, ok := strings.Cut(strings.TrimRight(line, "\r"), " ")
		if ok && name == ref && isObjectID(sha) {
			return sha
		}
	}
	return ""
}

// Worktrees lists the repository's worktrees: the main checkout, then each
// linked one by the name of its entry. The same list from any worktree.
func (c *Client) Worktrees() ([]Worktree, error) {
	var out []Worktree
	if main, ok := c.mainCheckout(); ok {
		head, err := c.headOf(c.commonDir, main)
		if err != nil {
			return nil, err
		}
		out = append(out, Worktree{Path: main, Head: head})
	}
	entries, err := os.ReadDir(filepath.Join(c.commonDir, "worktrees"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		gitDir := filepath.Join(c.commonDir, "worktrees", e.Name())
		raw, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
		if err != nil {
			continue // not a worktree entry: git skips it too
		}
		dir := filepath.Dir(filepath.Clean(strings.TrimSpace(string(raw))))
		head, err := c.headOf(gitDir, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, Worktree{Path: dir, Head: head})
	}
	return out, nil
}

// mainCheckout is the main checkout's top directory: the parent of the common
// `.git`, which a client of a bare repository or a submodule's git directory
// has none of unless it is itself the checkout.
func (c *Client) mainCheckout() (string, bool) {
	if filepath.Base(c.commonDir) == ".git" {
		return filepath.Dir(c.commonDir), true
	}
	return c.root, !c.linked
}

// mergeRefs are the refs an unfinished merge, cherry-pick or revert leaves,
// checked in this order.
var mergeRefs = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"}

// MergeInProgress names the first of MERGE_HEAD, CHERRY_PICK_HEAD and
// REVERT_HEAD that holds a commit in this worktree's git directory, or "".
func (c *Client) MergeInProgress() string {
	for _, ref := range mergeRefs {
		if raw, err := os.ReadFile(filepath.Join(c.gitDir, ref)); err == nil {
			if sha, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " "); isObjectID(sha) {
				return ref
			}
		}
	}
	return ""
}
