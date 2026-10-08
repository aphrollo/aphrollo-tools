package core

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/config/decl"
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
	// Env is KEY=value bindings RunSuite adds to the command's environment,
	// after everything it inherits, so a binding here wins. nil for every
	// runner but a proof's cargo one, which points the build at a target
	// directory outside the lane it copied.
	Env []string
	// Cached lets go's own test-result cache serve this `go test` run: the
	// gate's usual -count=1 is left off (test-cache in aphrollo.toml, chosen
	// per stage by suite.withTestCache). false, the default for every runner,
	// keeps -count=1 so a pass is a measurement of the tree on disk.
	Cached bool
	// Impure is the package patterns of a Cached run whose results go's cache
	// cannot vouch for (test-cache-impure): RunSuite runs those apart with
	// -count=1. nil when the run is not Cached.
	Impure []string
	// Select says how a `go test` run was narrowed to the tests that cover an
	// edit (test-select in aphrollo.toml, suite.withSelectedTests), or why it
	// was not. nil for every runner the setting did not look at.
	Select *Selection
}

// Selection is the account of one edit-time test selection. Reason set means
// the package ran whole and says why; otherwise Run of Total tests were named,
// the ones covering Funcs.
type Selection struct {
	Run, Total int
	Funcs      []string
	Reason     string
}

// selectionNamedFuncs is how many covered functions a gate line names; the
// rest are a count.
const selectionNamedFuncs = 3

// Note is the clause a gate line carries for the selection: "selected M of K
// tests: covering F1, F2" or "full suite: <reason>", "" for no selection.
func (s *Selection) Note() string {
	switch {
	case s == nil:
		return ""
	case s.Reason != "":
		return "full suite: " + s.Reason
	}
	named := s.Funcs
	more := ""
	if len(named) > selectionNamedFuncs {
		more = fmt.Sprintf(" and %d more", len(named)-selectionNamedFuncs)
		named = named[:selectionNamedFuncs]
	}
	return fmt.Sprintf("selected %d of %d tests: covering %s%s", s.Run, s.Total, strings.Join(named, ", "), more)
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

// tomlBoolIn reads one boolean key from one table of a TOML file, through the
// one reader of those tables (internal/config/decl): false for an absent key,
// an unreadable value, or an unreadable file.
func tomlBoolIn(path, table, key string) bool {
	v, _ := tomlBoolSetIn(path, table, key)
	return v
}

// tomlBoolSetIn is tomlBoolIn with the fact tomlBoolIn throws away: whether
// the key was set to a boolean. A default that differs from `false` needs to
// tell an absent key from one somebody set, and only the reader knows.
func tomlBoolSetIn(path, table, key string) (value, set bool) {
	return decl.Read(path, declTable(table)).Flag(key)
}

// tomlStringIn reads one scalar key from one table of a TOML file: a string as
// it is, a number or boolean as its text. "", false for an absent key or an
// unreadable file.
func tomlStringIn(path, table, key string) (value string, set bool) {
	return decl.Read(path, declTable(table)).Raw(key)
}

// tomlStringsIn reads one string-array key from one table of a TOML file,
// sorted and deduped; empty for an absent key or an unreadable file.
func tomlStringsIn(path, table, key string) []string {
	return dedupeSorted(decl.Read(path, declTable(table)).List(key))
}

// declTable is a table as the callers spell it, "[aphrollo]", without its
// brackets.
func declTable(table string) string { return strings.Trim(table, "[]") }

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
