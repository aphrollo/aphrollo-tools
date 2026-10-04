package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/git"
	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
)

// Every git call this package makes goes through internal/git's client: one
// client per worktree root for the life of the process, which reads what sits
// in files (HEAD, the branch, the worktree list, a ref, the trunk) from those
// files and asks the git binary for the rest. The verbs here are run by the
// operator, so the client inherits their environment (their GIT_SSH_COMMAND,
// their author) with terminal prompts off, and runs the `git` on PATH, the
// queue shim where `aphrollo install` put one.

var repoClients = struct {
	sync.Mutex
	byDir map[string]*git.Client
}{byDir: map[string]*git.Client{}}

// repoGit is the client for the repository dir sits in, or nil when it sits in
// none. The client is kept by directory; one whose worktree has gone, or been
// made again, is replaced.
func repoGit(dir string) *git.Client {
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	// git -C names a directory that exists; a path that does not is no repository,
	// not the one it sits inside.
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil
	}
	repoClients.Lock()
	defer repoClients.Unlock()
	if c, ok := repoClients.byDir[abs]; ok && stillThere(c) {
		return c
	}
	c, err := git.New(abs, git.Options{Inherit: true, Env: noPrompt, Timeout: lightCeiling})
	if err != nil {
		delete(repoClients.byDir, abs)
		return nil
	}
	repoClients.byDir[abs] = c
	return c
}

// stillThere reports whether the worktree and git directory a kept client
// reads still exist.
func stillThere(c *git.Client) bool {
	if _, err := os.Stat(c.GitDir()); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(c.Root(), ".git"))
	return err == nil
}

func notARepo(dir string) error {
	return fmt.Errorf("%s is not inside a git working tree", dir)
}

// wtGit runs git in the worktree dir sits in and answers its stdout; on
// failure the error is the child's *exec.ExitError.
func wtGit(dir string, args ...string) ([]byte, error) {
	c := repoGit(dir)
	if c == nil {
		return lightOutput(childrun.Spec{Name: "git", Args: append([]string{"-C", dir}, args...)})
	}
	out, err := c.Output(args...)
	return []byte(out), err
}

// wtGitCombined is wtGit answering stdout and stderr together.
func wtGitCombined(dir string, args ...string) ([]byte, error) {
	c := repoGit(dir)
	if c == nil {
		return lightCombined(childrun.Spec{Name: "git", Args: append([]string{"-C", dir}, args...)})
	}
	out, err := c.Combined(args...)
	return []byte(out), err
}

// wtGitCombinedFor is wtGitCombined under a deadline of its own.
func wtGitCombinedFor(dir string, timeout time.Duration, args ...string) ([]byte, error) {
	c := repoGit(dir)
	if c == nil {
		out, err := lightCombined(childrun.Spec{Name: "git", Args: append([]string{"-C", dir}, args...), Timeout: timeout})
		return out, networkTimeoutErr(errors.Is(err, childrun.ErrTimeout), timeout, "git", args, err)
	}
	out, err := c.CombinedFor(timeout, args...)
	return []byte(out), err
}

// wtGitStream runs git with its output going where the caller says.
func wtGitStream(dir string, stdout, stderr io.Writer, args ...string) error {
	return wtGitDo(dir, git.Call{Stdout: stdout, Stderr: stderr}, args...)
}

// wtGitOK reports whether git exited cleanly.
func wtGitOK(dir string, args ...string) bool {
	return wtGitDo(dir, git.Call{}, args...) == nil
}

// wtNetwork runs a git call that touches the network under gitNetworkTimeout,
// answering stdout and stderr together; a deadline that ended it is named.
func wtNetwork(dir string, args ...string) ([]byte, error) {
	timeout := gitNetworkTimeout
	c := repoGit(dir)
	if c == nil {
		return lightCombined(childrun.Spec{Name: "git", Args: append([]string{"-C", dir}, args...), Timeout: timeout})
	}
	out, err := c.CombinedFor(timeout, args...)
	return []byte(out), networkTimeoutErr(errors.Is(err, childrun.ErrTimeout), timeout, "git", args, err)
}

// wtNetworkStream is wtNetwork with the output streamed as it comes (a push's
// progress meter).
func wtNetworkStream(dir string, stdout, stderr io.Writer, args ...string) error {
	timeout := gitNetworkTimeout
	err := wtGitDo(dir, git.Call{Timeout: timeout, Stdout: stdout, Stderr: stderr}, args...)
	return networkTimeoutErr(errors.Is(err, childrun.ErrTimeout), timeout, "git", args, err)
}

// plainRefName reports whether ref is a ref name the client can answer from
// the ref files: no revision syntax, and not a commit's abbreviation.
func plainRefName(ref string) bool {
	if ref == "" {
		return false
	}
	hex := true
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'f', r >= '0' && r <= '9':
		case r >= 'g' && r <= 'z', r >= 'A' && r <= 'Z', r == '/', r == '-', r == '_', r == '.':
			hex = false
		default:
			return false
		}
	}
	return !hex || len(ref) < 4
}

// worktreeEntries lists every worktree of the repository dir sits in, the main
// checkout first, from the repository's files. A detached worktree's branch is
// "HEAD".
func worktreeEntries(dir string) ([]worktreeEntry, error) {
	c := repoGit(dir)
	if c == nil {
		return nil, notARepo(dir)
	}
	list, err := c.Worktrees()
	if err != nil {
		return nil, err
	}
	out := make([]worktreeEntry, 0, len(list))
	for _, w := range list {
		e := worktreeEntry{Path: filepath.Clean(w.Path), Branch: w.Head.Branch}
		if w.Head.Detached || e.Branch == "" {
			e.Branch = "HEAD"
		}
		out = append(out, e)
	}
	return out, nil
}

// wtRemoteURL is the URL of the named remote of the repository dir sits in, or
// "" when there is none.
func wtRemoteURL(dir, name string) string {
	if c := repoGit(dir); c != nil {
		return c.RemoteURL(name)
	}
	return ""
}

// wtHeadSHA is the commit dir's worktree has checked out, read from the files.
func wtHeadSHA(dir string) (string, error) {
	c := repoGit(dir)
	if c == nil {
		var out, stderr bytes.Buffer
		if err := wtGitDo(dir, git.Call{Stdout: &out, Stderr: &stderr}, "rev-parse", "HEAD"); err != nil {
			return "", headReadError(dir, err, stderr.String())
		}
		return strings.TrimSpace(out.String()), nil
	}
	h, err := c.Head()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %w", dir, err)
	}
	if h.SHA == "" {
		return "", fmt.Errorf("git rev-parse HEAD in %s: no commit yet", dir)
	}
	return h.SHA, nil
}

// gitBranchRef reports whether the full ref name resolves in dir's repository.
func gitBranchRef(dir, ref string) bool {
	c := repoGit(dir)
	return c != nil && c.ResolveRef(ref) != ""
}

// gitStepIn reads a plan step that runs git in a repository: the directory it
// works in (the -C it names, else the step's own) and its arguments after any
// -C. A step with no repository to work in is not one.
func gitStepIn(s Step) (dir string, args []string, ok bool) {
	if len(s.Cmd) < 2 || s.Cmd[0] != "git" {
		return "", nil, false
	}
	args = s.Cmd[1:]
	dir = s.Dir
	if args[0] == "-C" && len(args) >= 3 {
		dir, args = args[1], args[2:]
	}
	if dir == "" || repoGit(dir) == nil {
		return "", nil, false
	}
	return dir, args, true
}

// runGitStep runs a git step through the repository's client; a step that
// touches the network is held to the network deadline.
func runGitStep(s Step, dir string, args []string, stdout, stderr io.Writer) error {
	call := git.Call{Stdout: stdout, Stderr: stderr}
	if s.Network {
		call.Timeout = gitNetworkTimeout
	}
	err := repoGit(dir).Do(call, args...)
	if s.Network {
		return networkTimeoutErr(errors.Is(err, childrun.ErrTimeout), call.Timeout, "git", args, err)
	}
	return err
}

// abbrev is the short form of a commit id, seven characters as git prints it
// unless the repository is large enough to need more.
func abbrev(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// wtGitDo runs git once in dir's repository with the call's own deadline and
// writers; for a directory in no repository the binary runs there itself, so
// git's own words about it are what comes back.
func wtGitDo(dir string, call git.Call, args ...string) error {
	if c := repoGit(dir); c != nil {
		return c.Do(call, args...)
	}
	return lightRun(childrun.Spec{Name: "git", Args: append([]string{"-C", dir}, args...), Timeout: call.Timeout, Stdout: call.Stdout, Stderr: call.Stderr})
}

// headReadError words a failed read of HEAD with git's own stderr.
func headReadError(dir string, err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("git rev-parse HEAD in %s: %v: %s", dir, err, msg)
}
