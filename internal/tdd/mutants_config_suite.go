package tdd

import (
	"path/filepath"
)

// firstDeclaredList reads one string-array key from the first table that
// declares a non-empty one.
func firstDeclaredList(tables []mutantsConfigTable, key string) []string {
	for _, t := range tables {
		if entries := tomlStringsIn(t.path, t.table, key); len(entries) > 0 {
			return entries
		}
	}
	return nil
}

// mutantsConfigTable is one place a key may be written: a file and the table
// inside it. The Cargo spelling is tried first and aphrollo.toml is the
// fallback, the same precedence IssueLabels and mutation-baseline-exclude
// already use for a repo that may or may not be a Cargo workspace.
type mutantsConfigTable struct{ path, table string }

func mutantsConfigTables(root string) []mutantsConfigTable {
	ws := cargoWorkspaceRoot(root)
	if ws == "" {
		ws = root
	}
	return []mutantsConfigTable{
		{filepath.Join(ws, "Cargo.toml"), "[workspace.metadata.aphrollo]"},
		{filepath.Join(root, "aphrollo.toml"), "[aphrollo]"},
	}
}
