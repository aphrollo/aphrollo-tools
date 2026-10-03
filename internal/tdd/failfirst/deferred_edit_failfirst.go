package failfirst

import (
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// headSHAFor is the current commit of root's repo, "" outside a repo and
// before the first commit — the first half of "does this result describe the
// code on disk now". It is read from the repository's files, with no spawn.
func headSHAFor(root string) string {
	c := gitx.HookClient(root)
	if c == nil {
		return ""
	}
	head, err := c.Head()
	if err != nil {
		return ""
	}
	return head.SHA
}
