package postedit

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/lock"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/mutation"
)

// TestMain isolates the package's test run through tddtest.Main, like every
// internal/tdd package: gitx's git binary and git-queue variable, lock's
// build-lock dir, lock-dir resolver and lock-held variable, and mutation's
// busy-CI-runner probe, which the gates here reach through the mutants stage.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run:              func() int { return m.Run() },
		GitBinary:        gitx.GitBinary,
		GitQueuedEnv:     gitx.GitQueuedEnv,
		BuildLockHeldEnv: lock.BuildLockHeldEnv,
		SetLockDir:       lock.SetLockDirForTest,
		SetLockDirName:   lock.SetSharedLockDirForTest,
		SetCIRunnerJobs:  mutation.SetCIRunnerJobsForTest,
	}))
}
