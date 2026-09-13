package sqlc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points git's global and system config, and the gate's own state
// dir, at throwaway locations for the WHOLE package run. Without it every
// fixture commit inherits this box's core.hooksPath, runs the installed gate
// against a t.TempDir() tree, and appends the verdict to the operator's real
// gate.log — see TestFixtureGit_RunsNoneOfThisBoxsInstalledHooks. The same
// net internal/tdd and internal/cli already keep.
//
// The git identity is supplied per invocation by the fixture helper's
// GIT_AUTHOR_*/GIT_COMMITTER_* env, so an empty global config costs the
// fixtures nothing.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aphrollo-sqlc-pkgtest-")
	if err != nil {
		panic(err)
	}
	gitConfig := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(gitConfig, nil, 0o600); err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": gitConfig,
		"GIT_CONFIG_SYSTEM": os.DevNull,
		"CLAUDE_CONFIG_DIR": filepath.Join(dir, "claude"),
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
