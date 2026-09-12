package fixture

import (
	"bytes"
	"fmt"
	"os/exec"
)

// gitShowReported captures the child's stderr into a buffer of its own and
// reads it back into the error it returns.
func gitShowReported(ref string) (string, error) {
	cmd := exec.Command("git", "show", ref)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output() // stderr-ok: cmd.Stderr above is folded into the error below
	if err != nil {
		return "", fmt.Errorf("git show %s: %w: %s", ref, err, stderr.String())
	}
	return string(out), nil
}

// ghChecks is issue #652's reach: the only capture in this file belongs to
// gitShowReported above, and a twenty-line window let it vouch for this call
// too. Nothing here reads gh's own failure text back out, so it is lost, and
// the hit says so.
func ghChecks(branch string) ([]byte, error) {
	cmd := exec.Command("gh", "pr", "checks", "--", branch)
	out, err := cmd.Output()
	return out, err
}
