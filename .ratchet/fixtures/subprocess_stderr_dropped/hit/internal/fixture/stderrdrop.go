package fixture

import (
	"fmt"
	"os/exec"
)

// gitShow is the shape #183 recorded: any Output() failure collapses to
// "exit status N", and the real git stderr is never seen by the caller.
func gitShow(ref, path string) (string, error) {
	out, err := exec.Command("git", "show", ref+":"+path).Output()
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w", ref, path, err)
	}
	return string(out), nil
}
