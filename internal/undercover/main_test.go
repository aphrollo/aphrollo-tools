package undercover

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the run off from the box's git world. Run from inside a hook
// the fixtures' own `git init` and `git config` would otherwise act on the real
// repository instead of their temp dirs; see gitiso.Isolate.
func TestMain(m *testing.M) {
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}
