package fixture

import (
	"fmt"
	"os/exec"
)

// ghChecksNoted is issue #676's site, and it is a hit only under the matcher
// #682 corrected. This law's trigger is end-anchored, so against the line AS
// WRITTEN any trailing note at all defeated the match and removed this call
// from the law entirely: no hit, no finding, no baseline row. The note below
// is an ordinary remark, and a bare note never suppresses a hit — only the
// escape does, and it carries a reason that is recorded.
func ghChecksNoted(branch string) ([]byte, error) {
	out, err := exec.Command("gh", "pr", "checks", branch).Output() // gh's own failure text is lost here
	if err != nil {
		return nil, fmt.Errorf("gh pr checks %s: %w", branch, err)
	}
	return out, nil
}
