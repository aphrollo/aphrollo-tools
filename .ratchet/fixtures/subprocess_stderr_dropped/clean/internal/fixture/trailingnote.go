package fixture

import (
	"fmt"
	"os/exec"
	"strings"
)

// gitBlameNoted carries the same ordinary trailing note as its hit twin and is
// the other half of the pair: stripping the note makes the site VISIBLE to the
// law, and the marker window is what excuses it. The capture two lines up is
// read back into the returned error, so git's failure text survives — a newly
// visible site is judged on the law's own terms, not turned into a hit by the
// mere fact that it now matches the trigger.
func gitBlameNoted(path string) (string, error) {
	cmd := exec.Command("git", "blame", "--", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output() // the same note the hit twin carries
	if err != nil {
		return "", fmt.Errorf("git blame %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
