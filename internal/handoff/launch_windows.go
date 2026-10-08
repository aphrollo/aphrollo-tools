package handoff

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

// Launch runs path as a child on the same stdio and passes its exit code
// through: Windows has no exec that replaces the image.
func Launch(path string, args, env []string, announce func()) (int, error) {
	// Ctrl-C reaches the whole console: the child handles it and its exit
	// code passes through, so the parent must not die first.
	signal.Ignore(os.Interrupt)
	cmd := exec.Command(path, args...) // exec-ok: the handoff child must inherit stdio untouched
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	announce()
	err := cmd.Wait()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	}
	return 0, err
}

// OwnedByUser is true: the root is under the account's own LOCALAPPDATA.
func OwnedByUser(string) bool { return true }
