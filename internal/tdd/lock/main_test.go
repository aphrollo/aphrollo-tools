package lock

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// TestMain isolates the package's test run through tddtest.Main, like every
// internal/tdd package. lock owns the build-lock dir, the machine-wide lock
// dir's resolver and the lock-held marker, so its tests run under the same
// lock isolation and live lock-dir guard as the root package's; it has no
// CI probe or hooks install to hand over.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run:              func() int { return m.Run() },
		GitBinary:        gitx.GitBinary,
		GitQueuedEnv:     gitx.GitQueuedEnv,
		BuildLockHeldEnv: BuildLockHeldEnv,
		SetLockDir:       SetLockDirForTest,
		SetLockDirName:   SetSharedLockDirForTest,
	}))
}
