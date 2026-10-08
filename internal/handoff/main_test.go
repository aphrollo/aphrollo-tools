package handoff

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain doubles as the two helper programs the launch test runs: "launch"
// hands off to HANDOFF_TEST_TARGET, "fake" is the newer binary and exits 7.
func TestMain(m *testing.M) {
	switch os.Getenv("HANDOFF_TEST_MODE") {
	case "launch":
		code, err := Launch(os.Getenv("HANDOFF_TEST_TARGET"), []string{"workspace", "list"}, []string{"HANDOFF_TEST_MODE=fake", GuardEnv + "=1"}, func() {})
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(99)
		}
		os.Exit(code)
	case "fake":
		os.Stdout.WriteString("fake:" + os.Getenv(GuardEnv))
		os.Exit(7)
	}
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}
