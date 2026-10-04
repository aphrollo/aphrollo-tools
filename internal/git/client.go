// Package git is the one client for git. A Client is bound to one worktree
// root. It reads the facts that sit in files (HEAD, the git directories, the
// branch, the worktree list, a merge in progress) from those files, and asks the
// git binary for the rest: every spawn goes through internal/run as a light
// child in an environment scrubbed of the GIT_* variables a hook exports
// (gitenv.CleanFor), and is counted, so a caller and a test can see what a
// hook's work cost.
//
// A per-edit question needs one spawn at most: Status answers the staged,
// unstaged and untracked paths and the branch header in one
// `git status --porcelain=v2 -z --branch`, and is kept for the batch that
// asked.
package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// ErrNotRepo is what New says of a directory in no repository.
var ErrNotRepo = errors.New("git: not inside a git working tree")

// defaultTimeout bounds one git call whose Options give no limit: a hook
// cannot wait on git for the ten minutes run allows a light child.
const defaultTimeout = 30 * time.Second

// maxConcurrent caps the git children one process has running at once, so a
// fan-out cannot turn one hook into hundreds of children on a box already out
// of process slots (#997).
const maxConcurrent = 4

var slots = make(chan struct{}, maxConcurrent)

// Options tune a Client. The zero value is right for a caller with nothing
// special to say.
type Options struct {
	// Bin is the git to run; "git" when empty. A caller whose PATH leads with a
	// shim passes the real binary.
	Bin string
	// Env is added after the scrubbed environment: a marker the caller's own
	// shim reads, say.
	Env []string
	// Timeout ends one call; defaultTimeout when zero.
	Timeout time.Duration
	// Inherit runs git in this process's whole environment, GIT_* included,
	// plus Env. A verb the operator ran wants their GIT_SSH_COMMAND, their
	// GIT_AUTHOR_NAME and the like; a hook's client leaves it false and runs in an
	// environment scrubbed of them.
	Inherit bool
}

// Client is git for one worktree. It is safe for concurrent use.
type Client struct {
	root, gitDir, commonDir string
	linked, reftable        bool
	opt                     Options
	spawns                  atomic.Int64

	mu        sync.Mutex
	status    *Status
	statusKey string
	trunk     string
	trunkDone bool
	remotes   map[string]string
}

// New binds a Client to the worktree dir is in, found by walking up to the
// `.git` it holds, without a spawn. A directory in no repository is
// ErrNotRepo.
func New(dir string, opt Options) (*Client, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// walk-terminates: root becomes its parent each turn, and the walk returns at the filesystem root
	for {
		if c, err := open(root, opt); !errors.Is(err, os.ErrNotExist) {
			return c, err
		}
		// git finds a bare repository by the directory itself holding one, and
		// answers it has no work tree; it does not walk past it.
		if looksBare(root) {
			return nil, ErrNotRepo
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, ErrNotRepo
		}
		root = parent
	}
}

// open binds a Client to root when root holds a `.git`: a directory, or the
// file a linked worktree or a submodule has, which names the git directory. It
// answers os.ErrNotExist when root holds neither.
func open(root string, opt Options) (*Client, error) {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return nil, err
	}
	// An empty `.git` directory is not a repository to git: it keeps walking up.
	if info.IsDir() && !isFile(filepath.Join(dotGit, "HEAD")) {
		return nil, os.ErrNotExist
	}
	c := &Client{root: root, gitDir: dotGit, commonDir: dotGit, opt: opt}
	if !info.IsDir() {
		text, err := os.ReadFile(dotGit)
		if err != nil {
			return nil, err
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(text)), "gitdir:")
		if !ok {
			return nil, ErrNotRepo
		}
		c.gitDir = resolveAgainst(root, strings.TrimSpace(target))
		c.commonDir = c.gitDir
	}
	// A linked worktree's git directory names the one that holds the objects
	// and the refs, relative to itself.
	if text, err := os.ReadFile(filepath.Join(c.gitDir, "commondir")); err == nil {
		c.commonDir = resolveAgainst(c.gitDir, strings.TrimSpace(string(text)))
		c.linked = true
	}
	_, statErr := os.Stat(filepath.Join(c.commonDir, "reftable"))
	c.reftable = statErr == nil
	return c, nil
}

// resolveAgainst is target made absolute against base, cleaned.
func resolveAgainst(base, target string) string {
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	return filepath.Clean(target)
}

// Root is the worktree's top directory.
func (c *Client) Root() string { return c.root }

// GitDir is the worktree's own git directory: `.git` in a main checkout,
// `.git/worktrees/<name>` in a linked one.
func (c *Client) GitDir() string { return c.gitDir }

// CommonDir is the git directory that holds the objects and refs, the same for
// every worktree of the repository.
func (c *Client) CommonDir() string { return c.commonDir }

// IsLinkedWorktree reports a worktree added with `git worktree add`, as
// against the main checkout.
func (c *Client) IsLinkedWorktree() bool { return c.linked }

// Spawns is how many git children this client has started, those that failed
// included.
func (c *Client) Spawns() int { return int(c.spawns.Load()) }

// Output runs git in the worktree and answers its stdout. On failure the
// error is the child's *exec.ExitError, which carries what it wrote on stderr.
func (c *Client) Output(args ...string) (string, error) { return c.outputIn(c.root, args...) }

func (c *Client) outputIn(dir string, args ...string) (string, error) {
	c.spawns.Add(1)
	slots <- struct{}{}
	defer func() { <-slots }()
	out, err := run.LightOutput(c.spec(dir, Call{}, args))
	return string(out), err
}

// spec is the child one call runs as.
func (c *Client) spec(dir string, call Call, args []string) run.Spec {
	bin := c.opt.Bin
	if bin == "" {
		bin = "git"
	}
	timeout := call.Timeout
	if timeout <= 0 {
		timeout = c.opt.Timeout
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	var env []string
	if c.opt.Inherit {
		env = append(os.Environ(), c.opt.Env...)
	} else {
		env = append(gitenv.CleanFor(dir, args...), c.opt.Env...)
	}
	return run.Spec{Name: bin, Args: args, Dir: dir, Env: env, Timeout: timeout, Stdout: call.Stdout, Stderr: call.Stderr}
}

// statusArgs is the one call that answers a batch. Optional locks are off so a
// hook's status never holds the index against the user's own git, and untracked
// files are listed one by one (a new directory is its files, not itself) with
// renames detected whatever the repository's config says.
var statusArgs = []string{"--no-optional-locks", "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--renames"}

// Status is the tree's status for the batch named by key: one spawn the first
// time, and the same answer for every later call with that key. A different key
// reads the tree again and replaces the kept answer. An empty key is a read of
// its own, never kept, and leaves the kept answer as it was. A failed call is
// never kept: the next call asks again, and a caller that wants a failure to
// stand for its batch keeps it itself.
func (c *Client) Status(key string) (*Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key == "" {
		return c.readStatus()
	}
	if key == c.statusKey && c.status != nil {
		return c.status, nil
	}
	st, err := c.readStatus()
	if err != nil {
		c.status, c.statusKey = nil, ""
		return nil, err
	}
	c.status, c.statusKey = st, key
	return st, nil
}

// readStatus asks git for the status now.
func (c *Client) readStatus() (*Status, error) {
	out, err := c.Output(statusArgs...)
	if err != nil {
		return nil, err
	}
	return ParseStatus(out)
}

// Canonical is the one spelling of a path every caller compares: symbolic
// links, junctions and short names resolved, case as the filesystem has it,
// cleaned. A path that does not resolve (it is gone) is only cleaned.
func Canonical(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return filepath.Clean(path)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// looksBare reports whether dir is itself a git directory, as git's own
// discovery tests it: a HEAD file, an objects directory and a refs directory.
func looksBare(dir string) bool {
	return isFile(filepath.Join(dir, "HEAD")) && isDir(filepath.Join(dir, "objects")) && isDir(filepath.Join(dir, "refs"))
}
