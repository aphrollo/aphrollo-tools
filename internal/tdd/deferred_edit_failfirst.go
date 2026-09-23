package tdd

import (
	"os/exec"
	"strings"
)

// headSHAFor is the current commit of root's repo, "" outside a repo — the
// first half of "does this result describe the code on disk now".
func headSHAFor(root string) string {
	out, err := exec.Command(gitBinary(), "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
