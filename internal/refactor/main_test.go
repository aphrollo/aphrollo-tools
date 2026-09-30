package refactor

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the package's run off from the box's git world: the
// repositories around it, the environment a hook exports, and the operator's
// git config. See gitiso.Isolate.
func TestMain(m *testing.M) {
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}
