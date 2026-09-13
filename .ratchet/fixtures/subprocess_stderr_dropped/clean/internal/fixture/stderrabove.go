package fixture

import (
	"fmt"
	"os/exec"
	"strings"
)

// lsFilesCaptured is the shape #658 read as an offence and this fixture pins
// as clean: the capture is a CODE token on the line DIRECTLY ABOVE the call
// it feeds, which is where a reader looks for it. A marker walk that tests
// the comment run before the marker never reaches this line.
func lsFilesCaptured(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files under %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	return strings.Split(string(out), "\x00"), nil
}
