package tdd

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// TestMain isolates the WHOLE package's test run from the operator's real
// world through tddtest.Main, which every internal/tdd package's TestMain
// calls: see its doc for each net it puts up.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run: func() int {
			initFixture = tddtest.InitFixture()
			// ShimBypassLine reads this box's real $HOME and PATH via
			// exec.LookPath — a real box (this one included) can have the
			// shims installed while the shell running `go test` never
			// sourced the profile line that puts them on PATH, which is
			// exactly the state the check exists to name. Left wired to the
			// real function, every OTHER session-start test's output would
			// depend on this host's own setup; the dedicated tests in
			// shimresolve_test.go override this seam themselves to drive
			// ShimBypassLine's actual behavior.
			restoreShimBypass := SetShimBypassLineForTest(func(string) string { return "" })
			defer restoreShimBypass()
			return m.Run()
		},
		GitBinary:         gitBinary,
		HooksDirUnsafeEnv: HooksDirUnsafeEnv,
		BuildLockHeldEnv:  BuildLockHeldEnv,
		GitQueuedEnv:      GitQueuedEnv,
		SetLockDir:        SetLockDirForTest,
		SetLockDirName:    SetSharedLockDirForTest,
		SetCIRunnerJobs:   SetCIRunnerJobsForTest,
	}))
}
