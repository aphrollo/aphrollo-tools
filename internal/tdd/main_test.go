package tdd

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// fakeGitCommonDir is what this binary prints on STDOUT when it is standing in
// as `git` (see tddtest.Main). A fixed sentinel, so the test asserting on the
// hooks path built from it needs nothing from the real git.
const fakeGitCommonDir = tddtest.FakeGitCommonDir

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
		LockDirName:       &sharedLockDirName,
		SetCIRunnerJobs:   SetCIRunnerJobsForTest,
	}))
}
