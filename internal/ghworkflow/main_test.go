package ghworkflow

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the package's run off from the box's git world, as every
// package that can reach a repository does. See gitiso.Isolate.
func TestMain(m *testing.M) {
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}
