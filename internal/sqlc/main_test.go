package sqlc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the package's run off from the box's git world and points the
// gate's own state dir at a throwaway location for the WHOLE package run.
// Without it every fixture commit inherits this box's core.hooksPath, runs the
// installed gate against a t.TempDir() tree, and appends the verdict to the
// operator's real gate.log — see TestFixtureGit_RunsNoneOfThisBoxsInstalledHooks.
// See gitiso.Isolate for the git side.
//
// The git identity is supplied per invocation by the fixture helper's
// GIT_AUTHOR_*/GIT_COMMITTER_* env, so an empty global config costs the
// fixtures nothing.
func TestMain(m *testing.M) {
	dir, err := gitiso.MkRoot("aphrollo-sqlc-pkgtest-")
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
