package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// ConfigRoot is the directory the user's config.toml lives in, found the way
// the data root is: $TRELLIS_CONFIG, else the per-user local data directory
// where the platform has one, else ${XDG_CONFIG_HOME:-~/.config}, each with a
// trellis directory under it. "" when no home can be resolved.
func ConfigRoot() string {
	if dir := os.Getenv("TRELLIS_CONFIG"); dir != "" {
		// A relative root would follow each process's working directory.
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return dir
	}
	if dir := localAppData(); dir != "" {
		return filepath.Join(dir, "trellis")
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "trellis")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "trellis")
}

// RepoID is the id a repo goes by in the user's [repo."<id>"] sections: the
// name of its state directory, a hash of the repo's shared git directory, so
// every worktree of one repo has the one id. "" when root holds no repo. It
// reads the filesystem and spawns nothing: a hook asks it on every call.
func RepoID(root string) string {
	if common := CommonDir(root); common != "" {
		return core.RepoStateKey(common)
	}
	return ""
}

// CommonDir is the git directory every worktree of the repo at root shares: its own
// .git for a main checkout, the common directory a linked worktree names. "" when
// root holds no repo. It reads the filesystem and spawns nothing.
func CommonDir(root string) string {
	if root == "" {
		return ""
	}
	gitPath := filepath.Join(root, ".git")
	fi, err := os.Lstat(gitPath)
	if err != nil {
		return ""
	}
	if fi.IsDir() {
		return gitPath
	}
	data, err := os.ReadFile(gitPath)
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if err != nil || !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	common := gitdir
	if rel, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common = strings.TrimSpace(string(rel))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
	}
	return filepath.Clean(common)
}

// ForDir reads every layer for the repo dir stands in, the way a hook reads
// them: the repo is found by walking up for a .git entry, never by asking git.
// Outside any repo, only the built-in and user layers are read.
func ForDir(dir string) *Config {
	repo := ""
	if dir != "" {
		repo = compat.RepoRoot(dir)
	}
	return Load(Options{Repo: repo, RepoID: RepoID(repo)})
}
