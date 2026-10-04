package merge

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// What this package asks git for that sits in a file is read from the file, by
// the worktree's client, and asked of the binary only where that client cannot
// read it: a directory in no repository, or a name that is not a ref.

// refSHA is the commit the ref named by its short name (main, origin/main)
// resolves to in dir's repository, "" when it resolves to nothing.
func refSHA(dir, ref string) string {
	if c := gitx.HookClient(dir); c != nil {
		return c.Rev(ref)
	}
	return strings.TrimSpace(gitOut(dir, "rev-parse", ref))
}

// laneBranchOf is the branch dir's worktree has checked out, "" when its
// HEAD is detached or unreadable.
func laneBranchOf(dir string) string {
	c := gitx.HookClient(dir)
	if c == nil {
		return ""
	}
	h, err := c.Head()
	if err != nil || h.Detached {
		return ""
	}
	return h.Branch
}

// originURL is the URL of dir's origin remote, "" when it has none.
func originURL(dir string) string {
	if c := gitx.HookClient(dir); c != nil {
		return c.RemoteURL("origin")
	}
	return ""
}
