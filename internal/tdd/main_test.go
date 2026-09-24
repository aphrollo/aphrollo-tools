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
