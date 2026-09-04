package tdd

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Three issues were filed against the real repository by nobody — #155, #196
// and #197, all titled `false-positive: override:override-off r`, with the
// evidence `e` and an unfilled `closes-by: law | stage | demote check X` line.
// Those are the literal fixture values in this package's override tests, and
// #196 and #197 landed four seconds apart while mutation jobs were starting.
//
// The tests that reach the issue-filing path stub gh themselves, and every
// guard in front of it — ghAvailable, hasGitHubRemote, titleMentions — holds
// on the unmutated code. Under mutation those guards are exactly what gets
// inverted, and a test whose repo does have a GitHub remote then runs the
// real gh and files a real issue. That is not a defect a test can assert
// about itself: the escape count is a health signal read at session start,
// and a recorder that files placeholders on its own turns it into noise.
//
// So gh is made unreachable for the whole package, the same way TestMain
// already nets CLAUDE_CONFIG_DIR away from the operator's real gate-state and
// the lock dir away from their real %TEMP%. A mutated guard then reaches the
// stub, which files nothing anywhere.
func TestGh_ResolvesToTheStubForEveryTestInThePackage(t *testing.T) {
	stub, err := ghStubDir()
	if err != nil {
		t.Fatal(err)
	}

	found, err := exec.LookPath("gh")
	if err != nil {
		return // no gh resolvable at all is also unreachable, which is the point
	}

	if !strings.HasPrefix(filepath.Clean(found), filepath.Clean(stub)) {
		t.Errorf("gh resolves to %q, outside the package stub at %q — a mutated guard reaches the real GitHub and files a real issue", found, stub)
	}
}
