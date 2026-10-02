package ghworkflow

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the package's run off from the box's git world, as every
// package that can reach a repository does. See gitiso.Isolate. It also keeps
// the run's own venv away from the box's real python: no test finds one unless
// it names a fake. And when this binary was copied to a file named pip or
// python, it is that fake tool (fake_tools_test.go), not the test run.
func TestMain(m *testing.M) {
	if code, ok := fakeToolMain(); ok {
		os.Exit(code)
	}
	findPython = func() (string, bool) { return "", false }
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}
