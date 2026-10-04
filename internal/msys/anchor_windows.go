//go:build windows

package msys

import (
	"bufio"
	"os"
	"os/exec"
	"time"
)

// anchorStartWait bounds how long the anchor is waited for: a shell that has
// not said it is up by then is given up on, and the run goes on without it.
const anchorStartWait = 15 * time.Second

// startAnchor starts a shell that says "up" once MSYS has read the temp
// directory, then blocks reading its stdin. exited is closed when it is gone;
// release ends it. Both are inert when the shell could not be started.
func startAnchor() (release func(), exited <-chan struct{}) {
	done := make(chan struct{})
	none := func() (func(), <-chan struct{}) { close(done); return func() {}, done }
	sh, err := exec.LookPath("sh")
	if err != nil {
		return none()
	}
	// exec-ok: msys cannot import internal/run: gitiso starts it, and run's own tests run under gitiso.Main, so the import would be a cycle in test; the child has no output to leak and ends with its stdin pipe.
	cmd := exec.Command(sh, "-c", "echo up; exec cat >/dev/null")
	cmd.Dir = os.TempDir()
	in, err := cmd.StdinPipe()
	if err != nil {
		return none()
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return none()
	}
	if err := cmd.Start(); err != nil {
		return none()
	}
	go func() {
		_ = cmd.Wait() // the exit status of a shell that only waits for EOF says nothing
		close(done)
	}()
	up := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(out).ReadString('\n')
		up <- err == nil && line != ""
	}()
	select {
	case ok := <-up:
		if !ok {
			_ = in.Close()
			return func() {}, done
		}
	case <-time.After(anchorStartWait):
		_ = cmd.Process.Kill()
		return func() {}, done
	}
	return func() { _ = in.Close() }, done
}
