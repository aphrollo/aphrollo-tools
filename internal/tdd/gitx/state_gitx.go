package gitx

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// maxConcurrentGit caps the git children one gate process has running at once.
// A hook runs a handful of git calls in sequence; a runaway (or a fan-out added
// later) must not turn one hook into hundreds of children while the box is
// already out of process slots (#997).
const maxConcurrentGit = 4

// gitSlots holds one token per running git child.
var gitSlots = make(chan struct{}, maxConcurrentGit)

// exhaustion holds where the one-line notice goes and whether it was given.
var exhaustion = struct {
	sync.Mutex
	w    io.Writer
	done bool
}{w: os.Stderr}

// setExhaustionNotice points the notice at w, re-arms it, and returns the undo.
func setExhaustionNotice(w io.Writer) (restore func()) {
	exhaustion.Lock()
	prevW, prevDone := exhaustion.w, exhaustion.done
	exhaustion.w, exhaustion.done = w, false
	exhaustion.Unlock()
	return func() {
		exhaustion.Lock()
		exhaustion.w, exhaustion.done = prevW, prevDone
		exhaustion.Unlock()
	}
}

// noteIfExhausted prints one line, once per process, when err says the OS
// could not start a process or thread. The caller carries on without git's
// answer; a Go runtime that fails to create a thread on its own dies in the
// runtime and never reaches here, which is why the cap above exists.
func noteIfExhausted(err error) {
	if !proc.IsResourceExhausted(err) {
		return
	}
	exhaustion.Lock()
	defer exhaustion.Unlock()
	if exhaustion.done {
		return
	}
	exhaustion.done = true
	fmt.Fprintf(exhaustion.w, "aphrollo gate: the OS cannot start a process (%v); skipping the git check\n", err)
}

// outputGit is cmd.Output under the concurrency cap, saying so once when the OS
// is out of processes.
func outputGit(cmd *exec.Cmd) ([]byte, error) {
	gitSlots <- struct{}{}
	defer func() { <-gitSlots }()
	out, err := cmd.Output()
	noteIfExhausted(err)
	return out, err
}

// gitOut reads a git value from root. It runs outside the hook context (the
// PostToolUse fingerprint, not a pre-commit worktree), so it intentionally skips
// cleanGitEnv() — no inherited GIT_* vars to scrub here.
func gitOut(root string, args ...string) string {
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = root
	out, err := outputGit(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
