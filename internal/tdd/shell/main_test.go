package shell

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// TestMain isolates the package's test run through tddtest.Main, like every
// internal/tdd package. shell runs no git and holds no lock, so it hands over
// no seams at all.
func TestMain(m *testing.M) {
	os.Exit(tddtest.Main(m, tddtest.Seams{
		Run: func() int { return m.Run() },
	}))
}
