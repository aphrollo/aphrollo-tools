package gitenv_test

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// A commit hook exports GIT_DIR, and this package's maintenance test runs `git
// init` and `git config gc.auto 1` in a temp repo: with that GIT_DIR in the
// environment the keys landed in the real repository's config (#1043). Run
// under a hostile environment, the test must leave the repository around it
// untouched.
func TestMain_TheMaintenanceTestLeavesAHooksRepositoryUntouched(t *testing.T) {
	gitiso.VerifyNoLeak(t, "TestDisableMaintenance_OutranksTheReposOwnConfig")
}
