package escape

import (
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
// as the root package's.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run:              func() int { return m.Run() },
		GitBinary:        gitx.GitBinary,
		GitQueuedEnv:     gitx.GitQueuedEnv,
		BuildLockHeldEnv: lock.BuildLockHeldEnv,
		SetLockDir:       lock.SetLockDirForTest,
		SetLockDirName:   lock.SetSharedLockDirForTest,
	}))
}
