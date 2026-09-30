package cli

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// The probe is the child half of TestTestMain_KeepsBareGitCallsOffTheReposAndConfigAroundTheRun.
func TestGitIsolation_Probe(t *testing.T) { gitiso.Probe(t) }

// The gate runs this package's tests from a git hook and from inside a
// checkout: GIT_DIR and friends in the environment, the package directory
// inside a repository. A fixture's bare `git init --bare` then wrote the real
// repository's config (#1043). TestMain must leave every such call on the
// repository the test made.
func TestTestMain_KeepsBareGitCallsOffTheReposAndConfigAroundTheRun(t *testing.T) {
	gitiso.VerifyNoLeak(t, "TestGitIsolation_Probe")
}
