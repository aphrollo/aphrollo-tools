package handoff

import (
	"errors"
	"os"
	"os/exec"
)

// Launch runs path as a child on the same stdio and passes its exit code
// through: Windows has no exec that replaces the image.
func Launch(path string, args, env []string) (int, error) {
	cmd := exec.Command(path, args...) // exec-ok: the handoff child must inherit stdio untouched
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	}
	return 0, err
}

// ownedByUser is true: the root is under the account's own LOCALAPPDATA.
func ownedByUser(string) bool { return true }
