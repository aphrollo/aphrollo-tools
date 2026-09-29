package precommit

import (
	"os"
	"path/filepath"
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
// It also states the linter absent for the run (see the Run closure).
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run: func() int {
			// golangci-lint is absent for the whole run: the answer a box
			// without it gives, and the one every test not about the linter
			// wants stated. A test that needs it present installs its own
			// (withLinterPresent) and so runs serially.
			defer SetLookLinterForTest(func() bool { return false })()
			// Git is isolated once for the run, not per test, so a test that
			// builds its repos from the fixture helpers may call t.Parallel.
			dir, err := os.MkdirTemp("", "aphrollo-precommit-git-")
			if err != nil {
				panic(err)
			}
			defer os.RemoveAll(dir)
			defer tddtest.SharedGit(filepath.Join(dir, "gitconfig"))()
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
