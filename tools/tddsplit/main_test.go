package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the package's run off from the box's git world and points the
// gate's state dir at a throwaway location for the whole package run: the
// end-to-end tests commit in fixture repositories, and without this every such
// commit would inherit this box's core.hooksPath and run the installed gate
// against a temp tree. See gitiso.Isolate for the git side. The fixture helper
// supplies the git identity per invocation.
func TestMain(m *testing.M) {
	dir, err := gitiso.MkRoot("tddsplit-pkgtest-")
	if err != nil {
		panic(err)
	}
	if _, err := gitiso.Isolate(dir); err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude")); err != nil {
		panic(err)
	}
	code := m.Run()
	gitiso.RemoveAll(dir)
	os.Exit(code)
}
