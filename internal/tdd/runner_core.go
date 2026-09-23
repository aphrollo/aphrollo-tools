package tdd

import (
	"os"
	"sort"
	"strings"
	"time"
)

// Runner is a test command: a program and its arguments, run from the project
// root. Keeping it a plain value makes detection and narrowing pure and
// testable; only PostToolUse actually executes it.
type Runner struct {
	Cmd  string
	Args []string
	// Dir overrides the execution directory RunSuite uses: "" (the default
	// for every runner except a resolved cargo one) means "use whatever root
	// the caller passed" — RunSuite falls back to its root parameter. Only
	// cargoRunnerAt / precommitRoot's cargo branch set this, to the actual
	// cargo WORKSPACE root: a checked-in .config/nextest.toml and the
	// workspace's Cargo.lock live there, not in a member crate's own
	// directory, so a `-p <pkg>` cargo command must execute from there even
	// though state/mech-cache keys keep using the member crate's own root.
	Dir string
	// Deadline overrides how long RunSuite may run: zero (the default for
	// every runner except one that went through runCargoLocked) means "use
	// RunSuite's own configured timeout unchanged". runCargoLocked sets this
	// to start+stageBudget BEFORE it waits for the machine-wide cargo build
	// lock, so that wait carves OUT of the stage's own budget instead of
	// stacking additively on top of it (RunSuite then runs for whichever is
	// shorter: its configured timeout, or the time remaining until
	// Deadline).
	Deadline time.Time
}

// cargoPackageName reads a Cargo.toml's `[package]` name, "" when the file is
// missing or is a virtual (workspace-only) manifest. A line scanner is enough:
// the name key lives directly under [package] in any real manifest, and a
// parse miss only costs the full-suite fallback, never a wrong scope.
func cargoPackageName(manifest string) string {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return ""
	}
	inPackage := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inPackage = trimmed == "[package]"
			continue
		}
		if !inPackage {
			continue
		}
		if key, val, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == "name" {
			return strings.Trim(strings.TrimSpace(val), `"`)
		}
	}
	return ""
}

// tomlBoolIn reads one boolean key from one table of a TOML file. A line
// scanner suffices for the same reason cargoPackageName uses one: the key sits
// directly under its table in any real manifest, and a parse miss costs only
// the feature staying off.
func tomlBoolIn(path, table, key string) bool {
	v, _ := tomlBoolSetIn(path, table, key)
	return v
}

// tomlBoolSetIn is tomlBoolIn with the fact tomlBoolIn throws away: whether
// the key was WRITTEN. A default that differs from `false` needs to tell an
// absent key from one somebody set, and only the reader knows.
func tomlBoolSetIn(path, table, key string) (value, set bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	inTable := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == table
			continue
		}
		if !inTable {
			continue
		}
		k, val, found := strings.Cut(trimmed, "=")
		if found && strings.TrimSpace(k) == key {
			return strings.TrimSpace(val) == "true", true
		}
	}
	return false, false
}

// tomlStringIn reads one scalar string key from one table of a TOML file,
// quotes stripped. "", false for an absent key or an unreadable manifest —
// same line scanner and same reasoning as tomlBoolIn.
func tomlStringIn(path, table, key string) (value string, set bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	inTable := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == table
			continue
		}
		if !inTable {
			continue
		}
		k, val, found := strings.Cut(trimmed, "=")
		if found && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(val), `"`), true
		}
	}
	return "", false
}

// dedupeSorted returns names deduped and sorted, so an identical worktree
// always yields an identical argv — the mech cache keys on that command.
func dedupeSorted(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
