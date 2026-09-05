package tdd

import (
	"os"
	"path/filepath"
	"strings"
)

// cargo's own precedence for where a build writes: CARGO_TARGET_DIR (already
// checked before this runs), then `build.target-dir` in a project
// .cargo/config.toml, then the same key in the USER config, then
// <workspace>/target. resolveTargetDir used to stop at the env var and the
// default, so a repo (or an operator) configured through .cargo/config.toml
// had its REAL target dir go unresolved: the build-slot lock keyed on the
// wrong directory (concurrent builds into the real one never contended as
// intended), and gcStrayTargetDirs proposed the real, live target dir as a
// stray once it sat idle past --older-than (issue #285).
//
// A line scan, not `cargo metadata`: resolveTargetDir runs on every cargo
// invocation this gate makes, several times per commit, and a subprocess
// spawn on that path is exactly the cost cargoPackageName's own line
// scanner (same shape, same file) was written to avoid. A parse miss here
// only costs the pre-existing env/default fallback, never a wrong lock.
func cargoConfigTargetDir(workspaceRoot string) string {
	if dir := targetDirFromConfigFile(filepath.Join(workspaceRoot, ".cargo", "config.toml"), workspaceRoot); dir != "" {
		return dir
	}
	if dir := targetDirFromConfigFile(filepath.Join(workspaceRoot, ".cargo", "config"), workspaceRoot); dir != "" {
		return dir
	}
	home := cargoHomeDir()
	if home == "" {
		return ""
	}
	if dir := targetDirFromConfigFile(filepath.Join(home, "config.toml"), workspaceRoot); dir != "" {
		return dir
	}
	return targetDirFromConfigFile(filepath.Join(home, "config"), workspaceRoot)
}

// cargoHomeDir is where the user config lives: CARGO_HOME when set, else
// ~/.cargo, cargo's own default.
func cargoHomeDir() string {
	if home := strings.TrimSpace(os.Getenv("CARGO_HOME")); home != "" {
		return home
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cargo")
	}
	return ""
}

// targetDirFromConfigFile reads `target-dir` under a [build] table, "" when
// the file is missing, unreadable, or declares no such key. A relative value
// is resolved against base (the workspace root every cargo invocation this
// gate makes actually runs from).
func targetDirFromConfigFile(path, base string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inBuild := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inBuild = trimmed == "[build]"
			continue
		}
		if !inBuild {
			continue
		}
		key, val, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(key) != "target-dir" {
			continue
		}
		raw := strings.Trim(strings.TrimSpace(val), `"`)
		if raw == "" {
			return ""
		}
		if filepath.IsAbs(raw) {
			return filepath.Clean(raw)
		}
		return filepath.Clean(filepath.Join(base, raw))
	}
	return ""
}
