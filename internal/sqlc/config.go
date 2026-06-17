// Package sqlc guards the aphrollo-api sqlc-generated code against the two ways
// it drifts from a clean `sqlc generate`:
//
//   - `aphrollo sqlc check` regenerates each discovered config into a temp dir
//     and diffs the output against the committed tree, failing CI when a config
//     that is supposed to be clean has drifted.
//   - `aphrollo sqlc regen --scoped` applies ONLY the hunks that derive from a
//     query the working tree changed (vs origin/main), backing out the
//     pre-existing drift so a one-column change lands a one-column diff.
//
// The whole-schema models.go gotcha: sqlc emits models.go from the ENTIRE
// migrations schema, so any unrelated migration (a new CrmTicket column, a new
// table) changes models.go even when your query is untouched. `check` surfaces
// that as drift; `regen --scoped` classifies it as PRE-EXISTING DRIFT and leaves
// it alone.
//
// Gating policy. Some generated trees are intentionally hand-post-edited (the
// aphrollo-api sqlcgen package — see its sqlc.yaml header), so a clean regen
// will always differ. We do NOT want `check` to fail on those. The mechanism is
// an explicit per-config `clean: true|false` flag, declared in a committed
// sidecar `.aphrollo-sqlc.yaml` at the repo root (a SEPARATE file, so the sqlc
// configs keep their exact semantics). A config marked `clean: false` is
// reported-only: its drift is printed but never fails the check. The default,
// when a config is absent from the sidecar (or there is no sidecar), is
// `clean: true` — gated — so a newly added config can't silently skip the gate.
package sqlc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SQLEntry is one `sql:` list item from a sqlc config: the query source, the
// schema source, and the Go output dir, each a path relative to the repo root.
type SQLEntry struct {
	Queries string
	Schema  string
	Out     string
}

// Config is a discovered sqlc config plus its gating policy.
type Config struct {
	Path    string     // absolute path to the config file
	Name    string     // base name, e.g. "sqlc-ai.yaml"
	Repo    string     // absolute repo root the config lives in
	Entries []SQLEntry // one per `sql:` list item
	Clean   bool       // true = gated; false = reported-only (intentional post-edits)
}

// sidecarName is the committed per-repo file that declares which configs are
// clean (gated) vs reported-only. Absent ⇒ every config defaults to gated.
const sidecarName = ".aphrollo-sqlc.yaml"

// DiscoverConfigs finds every sqlc config in repo (files matching sqlc*.yaml /
// sqlc*.yml at the repo root), parses each, and applies the sidecar gating
// policy. Results are sorted by file name for deterministic output.
func DiscoverConfigs(repo string) ([]Config, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", abs, err)
	}
	clean := readSidecar(filepath.Join(abs, sidecarName))

	var cfgs []Config
	for _, e := range ents {
		if e.IsDir() || !isSqlcConfigName(e.Name()) {
			continue
		}
		path := filepath.Join(abs, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		entries, err := parseConfig(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		gated := true // default: gated
		if v, ok := clean[e.Name()]; ok {
			gated = v
		}
		cfgs = append(cfgs, Config{
			Path:    path,
			Name:    e.Name(),
			Repo:    abs,
			Entries: entries,
			Clean:   gated,
		})
	}
	sort.Slice(cfgs, func(i, j int) bool { return cfgs[i].Name < cfgs[j].Name })
	return cfgs, nil
}

// isSqlcConfigName reports whether name is a sqlc config file: sqlc*.yaml or
// sqlc*.yml. The sidecar (.aphrollo-sqlc.yaml) does not match — it starts with a
// dot and "aphrollo", not "sqlc".
func isSqlcConfigName(name string) bool {
	if !strings.HasPrefix(name, "sqlc") {
		return false
	}
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// parseConfig extracts the queries/schema/out triple for each `sql:` list item.
// It is a deliberately small, indentation-aware scanner for the regular subset
// of sqlc v2 config we care about — not a general YAML parser (this binary is
// zero-dependency). Each list item under `sql:` opens a new entry; within an
// entry the first queries:/schema:/out: lines are captured. out: lives under
// gen.go but is unique within an entry, so no nesting tracking is needed.
func parseConfig(data []byte) ([]SQLEntry, error) {
	var entries []SQLEntry
	inSQL := false
	cur := -1
	for _, raw := range strings.Split(string(data), "\n") {
		line := stripComment(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		// Top-level key (indent 0): enter/leave the sql block.
		if indent == 0 {
			inSQL = strings.HasPrefix(trimmed, "sql:")
			continue
		}
		if !inSQL {
			continue
		}
		// A list item ("- ...") opens a new sql entry.
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			entries = append(entries, SQLEntry{})
			cur = len(entries) - 1
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if trimmed == "" {
				continue
			}
		}
		if cur < 0 {
			continue
		}
		key, val, ok := splitKeyVal(trimmed)
		if !ok {
			continue
		}
		switch key {
		case "queries":
			if entries[cur].Queries == "" {
				entries[cur].Queries = val
			}
		case "schema":
			if entries[cur].Schema == "" {
				entries[cur].Schema = val
			}
		case "out":
			if entries[cur].Out == "" {
				entries[cur].Out = val
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no sql entries found")
	}
	for i, e := range entries {
		if e.Out == "" {
			return nil, fmt.Errorf("sql entry %d has no gen.go.out", i)
		}
	}
	return entries, nil
}

// readSidecar parses the gating sidecar into a name→clean map. A missing or
// unparseable sidecar yields an empty map (every config then defaults to gated).
func readSidecar(path string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var file string
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(stripComment(raw))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			file = ""
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if trimmed == "" {
				continue
			}
		}
		key, val, ok := splitKeyVal(trimmed)
		if !ok {
			continue
		}
		switch key {
		case "file":
			file = val
		case "clean":
			if file != "" {
				out[file] = val == "true"
			}
		}
	}
	return out
}

// splitKeyVal splits "key: value" into an unquoted key and value. Returns
// ok=false when there is no colon.
func splitKeyVal(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	val = unquote(strings.TrimSpace(s[i+1:]))
	return key, val, true
}

// stripComment removes a trailing unquoted "# comment". Values in sqlc configs
// don't contain '#', so a naive split on the first '#' that follows whitespace
// is safe; a leading "#" comment line becomes empty.
func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return strings.TrimRight(line[:i], " \t")
	}
	return line
}

// unquote strips a single pair of matching surrounding quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
