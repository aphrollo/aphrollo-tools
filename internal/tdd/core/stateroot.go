package core

import (
	"os"
	"path/filepath"
)

// StateRoot is the root of the per-repo state that outlives any one plugin
// install: $TRELLIS_DATA, else %LOCALAPPDATA%\trellis where Windows sets
// LOCALAPPDATA, else ${XDG_STATE_HOME:-~/.local/state}/trellis. Git hooks run
// from a terminal and the git shims both find it without the plugin. "" when
// no home can be resolved.
func StateRoot() string {
	if dir := os.Getenv("TRELLIS_DATA"); dir != "" {
		// A relative root would follow each hook's working directory.
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return dir
	}
	if dir := localAppData(); dir != "" {
		return filepath.Join(dir, "trellis")
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "trellis")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "trellis")
}

// noRepoKey names the state directory of an event that happened outside any
// repository (a hook run from a scratch directory).
const noRepoKey = "_norepo"

// RepoStateDir is <root>/state/<repo> for the repository whose git common dir
// is common, where <repo> is the first 16 hex digits of the sha256 of that
// path. An empty common dir is no repository and files under noRepoKey. "" when
// there is no state root.
func RepoStateDir(common string) string {
	root := StateRoot()
	if root == "" {
		return ""
	}
	key := noRepoKey
	if common != "" {
		key = repoStateKey(common)
	}
	return filepath.Join(root, "state", key)
}
