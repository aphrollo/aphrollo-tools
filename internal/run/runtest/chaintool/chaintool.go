// Package chaintool is a fake command for a test to put on PATH: one that
// starts the shell chain of runtest.BashChain and holds in it. It is apart from
// runtest because it needs shfake, which starts its own children through run,
// and run's own tests use runtest.
package chaintool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
	"github.com/aphrollo/aphrollo-tools/internal/shfake"
)

// Install puts a command called name first on PATH for the test. Each run of it
// starts runtest.BashChain and holds in it until ended. It answers the file the
// chain records its pids in and the path of the command, for a caller that runs
// it by path. A caller that runs the real tool by name and ends it at its limit
// proves the limit reaches the tool's grandchildren, with no tool installed and
// the same shell chain on every host.
func Install(t testing.TB, name string) (pidFile, tool string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.ToSlash(filepath.Join(dir, "pids"))
	shfake.Install(t, dir, name, "#!/bin/sh\nset -- '"+pidFile+"' wait\n"+runtest.BashChain)
	tool = filepath.Join(dir, name)
	if _, err := os.Stat(tool + ".exe"); err == nil {
		tool += ".exe"
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return pidFile, tool
}
