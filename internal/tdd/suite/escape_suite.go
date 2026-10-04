package suite

import (
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
)

// --- the GitHub half -------------------------------------------------------

// ghAvailable reports whether the GitHub CLI is installed. A var so a test
// can state "absent" without emptying PATH.
var ghAvailable = func() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// gitHubHost is the host port for the repository dir sits in: the one way the
// gate speaks to GitHub. Every call runs in the sealed environment (no GIT_*
// variable, so a hook's GIT_DIR cannot make gh resolve the hook's repository),
// with terminal prompts off, and a failure carries gh's own STDERR: "exit status
// 1" names none of the three things that actually go wrong here (a label that
// does not exist, no auth, no network), and the operator cannot act on a verdict
// that does not say which.
//
// A zero timeout is no deadline: the escape verbs are typed by a human who can
// see them run, while the session-start line is on a path nothing is allowed to
// stall and so passes one.
func gitHubHost(dir string, timeout time.Duration) host.Host {
	if timeout <= 0 {
		timeout = -1
	}
	return github.New(github.Options{Dir: dir, Timeout: timeout, Env: cleanGitEnv()})
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
