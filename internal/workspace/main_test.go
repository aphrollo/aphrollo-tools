package workspace

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// The workspace verbs drive a Linux dev tier (systemd units, symlinks, POSIX
// paths); the suite execs fake binaries and compares slash paths, so on Windows
// it is not a test of anything and is skipped whole.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipping internal/workspace on windows: Linux dev-tier suite")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
