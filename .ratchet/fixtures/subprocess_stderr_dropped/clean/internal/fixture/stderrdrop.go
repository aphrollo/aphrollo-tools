package fixture

import (
	"errors"
	"fmt"
	"os/exec"
)

// gitShowChecked reads the child's stderr back out of the ExitError and
// folds it into the returned error, so the failure text survives.
func gitShowChecked(ref, path string) (string, error) {
	out, err := exec.Command("git", "show", ref+":"+path).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("git show %s:%s: %s", ref, path, exitErr.Stderr)
		}
		return "", err
	}
	return string(out), nil
}

// gitShowCombined never triggers at all: CombinedOutput folds stderr into
// the same stream, so there is nothing left to drop.
func gitShowCombined(ref, path string) (string, error) {
	out, err := exec.Command("git", "show", ref+":"+path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w", ref, path, err)
	}
	return string(out), nil
}

// gitVersion is the genuine exception: the exit code alone is the whole
// signal this caller ever uses, and the escape says so.
func gitVersion() bool {
	_, err := exec.Command("git", "--version").Output() // stderr-ok: presence check, exit code is the only signal used
	return err == nil
}
