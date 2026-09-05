package sqlc

import (
	"os"
	"path/filepath"
	"testing"
)

const aiCfg = `# ISOLATED sqlc config for the AI chat domain.
version: "2"
sql:
  - engine: "postgresql"
    queries: "queries/ai.sql"
    schema: "migrations"
    gen:
      go:
        package: "aigen"
        out: "internal/store/postgres/aigen"
        sql_package: "pgx/v5"
`

const mainCfg = `version: "2"
sql:
  - engine: "postgresql"
    queries: "queries"
    schema: "migrations"
    gen:
      go:
        package: "sqlcgen"
        out: "internal/store/postgres/sqlcgen"
`

func TestParseConfigExtractsEntry(t *testing.T) {
	entries, err := parseConfig([]byte(aiCfg))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 sql entry, got %d", len(entries))
	}
	e := entries[0]
	if len(e.Queries) != 1 || e.Queries[0] != "queries/ai.sql" {
		t.Errorf("queries = %v, want [queries/ai.sql]", e.Queries)
	}
	if len(e.Schema) != 1 || e.Schema[0] != "migrations" {
		t.Errorf("schema = %v, want [migrations]", e.Schema)
	}
	if e.Out != "internal/store/postgres/aigen" {
		t.Errorf("out = %q, want internal/store/postgres/aigen", e.Out)
	}
}

// sqlc v2 allows queries:/schema: to be a YAML list instead of a scalar path —
// each of the config's isolated domains can name several query files or
// several schema directories in one sql entry. The old scanner captured only
// a colon-bearing "key: value" line, so a list item ("- path", no colon)
// silently fell through unmatched and Queries/Schema stayed empty.
const listFormCfg = `version: "2"
sql:
  - engine: "postgresql"
    queries:
      - "queries/a.sql"
      - "queries/b.sql"
    schema:
      - "migrations/a"
      - "migrations/b"
    gen:
      go:
        package: "listgen"
        out: "internal/store/postgres/listgen"
`

func TestParseConfig_ParsesListFormQueriesAndSchema(t *testing.T) {
	entries, err := parseConfig([]byte(listFormCfg))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 sql entry, got %d", len(entries))
	}
	e := entries[0]
	wantQueries := []string{"queries/a.sql", "queries/b.sql"}
	if len(e.Queries) != len(wantQueries) || e.Queries[0] != wantQueries[0] || e.Queries[1] != wantQueries[1] {
		t.Errorf("queries = %v, want %v", e.Queries, wantQueries)
	}
	wantSchema := []string{"migrations/a", "migrations/b"}
	if len(e.Schema) != len(wantSchema) || e.Schema[0] != wantSchema[0] || e.Schema[1] != wantSchema[1] {
		t.Errorf("schema = %v, want %v", e.Schema, wantSchema)
	}
	if e.Out != "internal/store/postgres/listgen" {
		t.Errorf("out = %q, want internal/store/postgres/listgen", e.Out)
	}
}

// An entry with no queries path at all (list or scalar) must be refused at
// parse time rather than silently producing an empty Queries — an empty value
// reaching queryFiles resolves to the repo root and walks the whole tree.
const emptyQueriesCfg = `version: "2"
sql:
  - engine: "postgresql"
    schema: "migrations"
    gen:
      go:
        out: "internal/store/postgres/noqueries"
`

func TestParseConfig_ErrorsOnEmptyQueries(t *testing.T) {
	if _, err := parseConfig([]byte(emptyQueriesCfg)); err == nil {
		t.Fatal("parseConfig with no queries: value: want error, got nil")
	}
}

// A real config nests an `overrides:` list whose `- db_type:` items must NOT be
// mistaken for new top-level sql entries.
const aiCfgWithOverrides = `version: "2"
sql:
  - engine: "postgresql"
    queries: "queries/ai.sql"
    schema: "migrations"
    gen:
      go:
        package: "aigen"
        out: "internal/store/postgres/aigen"
        sql_package: "pgx/v5"
        overrides:
          - db_type: "uuid"
            go_type:
              import: "github.com/google/uuid"
              type: "UUID"
          - db_type: "timestamptz"
            go_type:
              import: "time"
              type: "Time"
`

func TestParseConfigIgnoresNestedOverrideLists(t *testing.T) {
	entries, err := parseConfig([]byte(aiCfgWithOverrides))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("overrides list items leaked into entries: got %d, want 1", len(entries))
	}
	if entries[0].Out != "internal/store/postgres/aigen" {
		t.Errorf("out = %q, want internal/store/postgres/aigen", entries[0].Out)
	}
}

func TestDiscoverConfigsFindsBoth(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "sqlc.yaml"), mainCfg)
	mustWrite(t, filepath.Join(repo, "sqlc-ai.yaml"), aiCfg)
	mustWrite(t, filepath.Join(repo, "README.md"), "not a config")

	cfgs, err := DiscoverConfigs(repo)
	if err != nil {
		t.Fatalf("DiscoverConfigs: %v", err)
	}
	if len(cfgs) != 2 {
		t.Fatalf("want 2 configs, got %d", len(cfgs))
	}
	// Sorted by name for determinism: sqlc-ai.yaml, sqlc.yaml.
	if cfgs[0].Name != "sqlc-ai.yaml" || cfgs[1].Name != "sqlc.yaml" {
		t.Errorf("configs not sorted by name: %q, %q", cfgs[0].Name, cfgs[1].Name)
	}
	// Default gating: clean (gated) when no sidecar entry says otherwise.
	for _, c := range cfgs {
		if !c.Clean {
			t.Errorf("%s: default Clean = false, want true (gated by default)", c.Name)
		}
	}
}

func TestSidecarMarksReportedOnly(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "sqlc.yaml"), mainCfg)
	mustWrite(t, filepath.Join(repo, "sqlc-ai.yaml"), aiCfg)
	mustWrite(t, filepath.Join(repo, ".aphrollo-sqlc.yaml"), `configs:
  - file: sqlc.yaml
    clean: false
  - file: sqlc-ai.yaml
    clean: true
`)

	cfgs, err := DiscoverConfigs(repo)
	if err != nil {
		t.Fatalf("DiscoverConfigs: %v", err)
	}
	byName := map[string]Config{}
	for _, c := range cfgs {
		byName[c.Name] = c
	}
	if byName["sqlc.yaml"].Clean {
		t.Errorf("sqlc.yaml should be reported-only (clean=false) per sidecar")
	}
	if !byName["sqlc-ai.yaml"].Clean {
		t.Errorf("sqlc-ai.yaml should be gated (clean=true) per sidecar")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
