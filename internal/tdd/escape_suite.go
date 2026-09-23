package tdd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --- the GitHub half -------------------------------------------------------

// ghAvailable reports whether the GitHub CLI is installed. A var so a test
// can state "absent" without emptying PATH.
var ghAvailable = func() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// runGh runs the GitHub CLI in dir and returns its stdout. A failure carries
// gh's own STDERR: "exit status 1" names none of the three things that
// actually go wrong here (a label that does not exist, no auth, no network),
// and the operator cannot act on a verdict that does not say which.
func runGh(dir string, args ...string) (string, error) {
	return runGhTimeout(dir, 0, args...)
}

// runGhTimeout is runGh with a deadline. Zero means none: the escape verbs
// are typed by a human who can see them run, while the session-start line is
// on a path nothing is allowed to stall.
func runGhTimeout(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.Background(), context.CancelFunc(func() {})
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return string(out), fmt.Errorf("gh %s: %w: %s", args[0], err, fitRunes(said, 400))
		}
		return string(out), fmt.Errorf("gh %s: %w", args[0], err)
	}
	return string(out), nil
}

// hasGitHubRemote reports whether repo pushes to GitHub. A repo that does not
// gets the local record and nothing else — there is nowhere to open an issue.
func hasGitHubRemote(repo string) bool {
	if repo == "" {
		return false
	}
	out, err := gitRead(repo, "remote", "-v")
	if err != nil {
		return false
	}
	return strings.Contains(out, "github.com")
}
