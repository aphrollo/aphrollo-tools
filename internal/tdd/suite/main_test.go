package suite

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/lock"
)

// TestMain isolates the package's test run through tddtest.Main, like every
// internal/tdd package. suite runs cargo and go under the build locks, so its
// tests get the same lock isolation and live lock-dir guard as lock's own,
// through lock's setters; it has no CI probe or hooks install to hand over.
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
