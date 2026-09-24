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
//
// The detached-phase spawner is refused for every test that does not install
// its own: the real one execs os.Executable() — the TEST binary here, which
// reads `gate runphase --job` as nothing and runs the whole package again,
// spawning further copies of itself.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run: func() int {
			spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) { return j, false }
			return m.Run()
		},
		GitBinary:        gitx.GitBinary,
		GitQueuedEnv:     gitx.GitQueuedEnv,
		BuildLockHeldEnv: lock.BuildLockHeldEnv,
		SetLockDir:       lock.SetLockDirForTest,
		SetLockDirName:   lock.SetSharedLockDirForTest,
		SetCIRunnerJobs:  mutation.SetCIRunnerJobsForTest,
	}))
}
