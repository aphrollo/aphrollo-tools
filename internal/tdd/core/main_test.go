package core

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// TestMain isolates the package's test run through tddtest.Main, like every
// internal/tdd package. core holds no build lock, lock dir or CI probe, so
// only the git seams are handed over.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run:          func() int { return m.Run() },
		GitBinary:    gitx.GitBinary,
		GitQueuedEnv: gitx.GitQueuedEnv,
	}))
}
