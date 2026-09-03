package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScopesFile is where a repo names reusable file sets, so several laws that
// share a boundary (Tier-1, presentation, …) state the glob list once and
// reference it from `[scope] alias = "<name>"` instead of repeating it.
const ScopesFile = ".ratchet/scopes.toml"

// LoadScopeSets reads <root>/.ratchet/scopes.toml's `[sets]` table: each key
// is a named, non-empty list of include globs. A repo that declares no
// aliases loads zero sets and no error — the file is opt-in, like the laws
// it sits beside.
func LoadScopeSets(root string) (map[string][]string, error) {
	path := filepath.Join(root, filepath.FromSlash(ScopesFile))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ScopesFile, err)
	}
	doc, err := parseTOML(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ScopesFile, err)
	}
	for _, section := range doc.sectionNames() {
		if section != "sets" {
			return nil, fmt.Errorf("%s: unknown table [%s] — scopes.toml declares [sets] only", ScopesFile, section)
		}
	}
	if root := doc.keys(""); len(root) > 0 {
		return nil, fmt.Errorf("%s: unknown root key %q — scopes.toml declares [sets] only", ScopesFile, root[0])
	}
	sets := map[string][]string{}
	for _, name := range doc.keys("sets") {
		v, _ := doc.value("sets", name)
		if v.kind != tomlArray || len(v.list) == 0 {
			return nil, fmt.Errorf("%s: sets.%s is a non-empty array of globs", ScopesFile, name)
		}
		sets[name] = v.list
	}
	return sets, nil
}

// resolveScopeAliases fills in Include for every law whose scope names an
// alias, by prepending the named set's globs to whatever Include the law
// also declares. A law naming an alias no set defines is a hard error — the
// one-line message names both the law and the alias, so the fix is obvious
// without opening scopes.toml.
func resolveScopeAliases(laws []Law, sets map[string][]string) error {
	for i := range laws {
		alias := laws[i].Scope.Alias
		if alias == "" {
			continue
		}
		set, ok := sets[alias]
		if !ok {
			return fmt.Errorf("%s: [scope].alias = %q — no such set in %s", laws[i].Name, alias, ScopesFile)
		}
		merged := make([]string, 0, len(set)+len(laws[i].Scope.Include))
		merged = append(merged, set...)
		merged = append(merged, laws[i].Scope.Include...)
		laws[i].Scope.Include = merged
	}
	return nil
}
