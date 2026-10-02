package rollback

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the gate state dir at a temp dir for the whole run, so a test
// that forgets its own t.Setenv still never reads or writes the operator's pin
// or event log.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aphrollo-rollback-pkgtest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
