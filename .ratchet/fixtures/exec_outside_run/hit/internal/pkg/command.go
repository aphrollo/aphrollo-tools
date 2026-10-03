package pkg

import "os/exec"

// gitHead starts git itself: no timeout, no guard over what git starts.
func gitHead(dir string) ([]byte, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	return cmd.Output()
}
