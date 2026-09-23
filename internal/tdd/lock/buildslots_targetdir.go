package lock

import (
	"os"
	"path/filepath"
	"strings"
)

// cargo's own precedence for where a build writes: CARGO_TARGET_DIR (already
// checked before this runs), then `build.target-dir` from the hierarchical
// walk of every ancestor .cargo/config.toml from the workspace root to the
// filesystem root, then the same key in the USER config, then
// <workspace>/target. resolveTargetDir used to stop at the env var and the
// default, so a repo (or an operator) configured through .cargo/config.toml
// had its REAL target dir go unresolved: the build-slot lock keyed on the
// wrong directory (concurrent builds into the real one never contended as
// intended), and gcStrayTargetDirs proposed the real, live target dir as a
// stray once it sat idle past --older-than (issue #285). Stopping the walk
// at the workspace root itself missed the same value declared one level
// ABOVE it -- a documented cargo mechanism and a normal monorepo/CI-mount
// arrangement -- which left the stray-target interlock (issue #285's fix)
// keying its lock on a path the live builder never actually resolved to,
// so nothing ever contended it (issue #422).
//
// A line scan, not `cargo metadata`: resolveTargetDir runs on every cargo
// invocation this gate makes, several times per commit, and a subprocess
// spawn on that path is exactly the cost cargoPackageName's own line
// scanner (same shape, same file) was written to avoid. A parse miss here
// only costs the pre-existing env/default fallback, never a wrong lock.
func cargoConfigTargetDir(workspaceRoot string) string {
	if dir := targetDirFromAncestorCargoConfigs(workspaceRoot); dir != "" {
		return dir
	}
	// cargoConfigPath (buildslots.go) is the same $CARGO_HOME/else-~/.cargo
	// resolution totalCargoJobs already uses for the user config -- reused
	// rather than re-derived, so the two readers can never disagree about
	// where the user config lives.
	userToml := cargoConfigPath()
	if userToml == "" {
		return ""
	}
	if dir := targetDirFromConfigFile(userToml, workspaceRoot); dir != "" {
		return dir
	}
	legacy := filepath.Join(filepath.Dir(userToml), "config")
	return targetDirFromConfigFile(legacy, workspaceRoot)
}

// targetDirFromAncestorCargoConfigs walks from workspaceRoot through every
// ancestor directory up to the filesystem root, the same hierarchical
// structure cargo itself resolves project config from
// (https://doc.rust-lang.org/cargo/reference/config.html#hierarchical-structure):
// the level closest to workspaceRoot wins for a scalar key like
// build.target-dir, so the walk returns on the first ancestor whose
// .cargo/config.toml (or the legacy extensionless .cargo/config) declares
// one. Returns "" once the walk reaches the root with no answer, leaving
// the $CARGO_HOME fallback to the caller.
func targetDirFromAncestorCargoConfigs(workspaceRoot string) string {
	dir := workspaceRoot
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		if v := targetDirFromConfigFile(filepath.Join(dir, ".cargo", "config.toml"), workspaceRoot); v != "" {
			return v
		}
		if v := targetDirFromConfigFile(filepath.Join(dir, ".cargo", "config"), workspaceRoot); v != "" {
			return v
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
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
	raw, ok := cargoConfigTableValue(string(data), "[build]", "target-dir")
	if !ok {
		return ""
	}
	raw = strings.Trim(raw, `"`)
	if raw == "" {
		return ""
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(base, raw))
}

// cargoConfigTableValue reads one key's raw value (comment-stripped, still
// quoted) under a named TOML table header from an already-read config's
// text. Shared by every .cargo/config.toml reader in this package —
// cargoConfigJobs (buildslots.go) used to do this same scan on its own and
// missed exactly the case this one now handles: `key = "value"  # comment`
// left `TrimSpace` seeing a trailing comment character, not the closing
// quote, so `strings.Trim(v, `+"`\"`"+`)` stripped only the LEADING quote and
// silently fed a bogus path (the value plus its trailing quote and comment
// text) to every caller -- a confident wrong answer, not the documented
// parse-miss fallback, and this reader's answer feeds build-slot and GC
// deletion decisions.
func cargoConfigTableValue(data, table, key string) (string, bool) {
	inTable := false
	for line := range strings.Lines(data) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inTable = strings.HasPrefix(trimmed, table)
			continue
		}
		if !inTable {
			continue
		}
		k, val, found := strings.Cut(trimmed, "=")
		if !found || strings.TrimSpace(k) != key {
			continue
		}
		val, _, _ = strings.Cut(val, "#")
		return strings.TrimSpace(val), true
	}
	return "", false
}
