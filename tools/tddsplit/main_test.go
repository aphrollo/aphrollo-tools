package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points git's global and system config and the gate's state dir at
// throwaway locations for the whole package run: the end-to-end tests commit
// in fixture repositories, and without this every such commit would inherit
// this box's core.hooksPath and run the installed gate against a temp tree.
// The fixture helper supplies the git identity per invocation.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tddsplit-pkgtest-")
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
