package pkg

import "os/exec"

// lookup answers from PATH and starts nothing the OS keeps running.
func lookup() *exec.Cmd {
	// exec-ok: a Cmd handed to the caller to configure, started by run.
	return exec.Command("git", "--version")
}
