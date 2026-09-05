package cli

import "testing"

// TestSurface_TablesMarkAliasesAsSuch proves each table flags a retiring
// spelling with Alias:true and names the verb it retires in favor of,
// while the canonical spelling it names stays Alias:false — so a reader (or
// a future table edit) can't silently drop the distinction the dispatch
// itself draws between the two.
func TestSurface_TablesMarkAliasesAsSuch(t *testing.T) {
	find := func(table []Verb, name string) (Verb, bool) {
		for _, v := range table {
			if v.Name == name {
				return v, true
			}
		}
		return Verb{}, false
	}

	cases := []struct {
		table     []Verb
		tableName string
		alias     string
		of        string
	}{
		{topLevelVerbTable, "top-level", "tdd", "gate"},
		{gateVerbTable, "gate", "init", "install"},
		{gateVerbTable, "gate", "install", "install"},
		{gateVerbTable, "gate", "issue", "issue"},
		{gateVerbTable, "gate", "primary-edits", "allow/revoke"},
		{gateVerbTable, "gate", "premergecommit", "premerge"},
		{workspaceVerbTable, "workspace", "update", "rebase"},
		{workspaceVerbTable, "workspace", "verify", "check"},
	}
	for _, c := range cases {
		v, ok := find(c.table, c.alias)
		if !ok {
			t.Fatalf("%s table has no entry %q", c.tableName, c.alias)
		}
		if !v.Alias {
			t.Errorf("%s table: %q should be marked Alias:true", c.tableName, c.alias)
		}
		if v.Of != c.of {
			t.Errorf("%s table: %q.Of = %q, want %q", c.tableName, c.alias, v.Of, c.of)
		}
	}

	canonical := []struct {
		table     []Verb
		tableName string
		name      string
	}{
		{topLevelVerbTable, "top-level", "gate"},
		{topLevelVerbTable, "top-level", "install"},
		{topLevelVerbTable, "top-level", "issue"},
		{gateVerbTable, "gate", "allow"},
		{gateVerbTable, "gate", "revoke"},
		{gateVerbTable, "gate", "premerge"},
		{workspaceVerbTable, "workspace", "rebase"},
		{workspaceVerbTable, "workspace", "pr"},
		{workspaceVerbTable, "workspace", "ship"},
	}
	for _, c := range canonical {
		v, ok := find(c.table, c.name)
		if !ok {
			t.Fatalf("%s table has no entry %q", c.tableName, c.name)
		}
		if v.Alias {
			t.Errorf("%s table: %q is the canonical spelling, should not be marked Alias", c.tableName, c.name)
		}
	}
}
