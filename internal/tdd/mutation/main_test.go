package mutation

import (
	context "context"
	"errors"
	io "io"
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/lock"
)

// TestMain isolates the package's test run through tddtest.Main, like every
// internal/tdd package: gitx's git binary and git-queue variable, and lock's
// build-lock dir, lock-dir resolver and lock-held variable, so a test here
// that reaches a build lock gets the same isolation and live lock-dir guard
// as the root package's. mutation also owns the busy-CI-runner probe every
// measurement waits on, and stands it down for the run as the root does.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run: func() int {
			// No unit test here compiles or runs a package's tests for coverage: the
			// commit stage asks the seam, and the seam answers that it cannot.
			testMapExecFn = refuseCoverageExec
			goEnvFn = func(context.Context, string) (string, error) { return "go-unit-test\n", nil }
			goListFn = func(context.Context, string, string) (string, error) { return "", nil }
			return m.Run()
		},
		GitBinary:        gitx.GitBinary,
		GitQueuedEnv:     gitx.GitQueuedEnv,
		BuildLockHeldEnv: lock.BuildLockHeldEnv,
		SetLockDir:       lock.SetLockDirForTest,
		SetLockDirName:   lock.SetSharedLockDirForTest,
		SetCIRunnerJobs:  SetCIRunnerJobsForTest,
	}))
}

// ratchet: test_removed internal/tdd/mutation/mutants_testmap_once_test.go: the detached test-map build and its once-per-repository lock are gone

// refuseCoverageExec is the coverage build's command seam in a unit test that
// has not installed a fake toolchain: every command fails to start.
func refuseCoverageExec(context.Context, string, []string, []string, io.Writer) (int, error) {
	return 1, errors.New("no toolchain in a unit test")
}

// ratchet: test_removed internal/tdd/mutation/mutants_commit_edit_test.go: the edit-time mutation run (gate mutants edit) and its result file are gone; mutation runs at commit only
