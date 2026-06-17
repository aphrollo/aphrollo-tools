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
	if e.Queries != "queries/ai.sql" {
		t.Errorf("queries = %q, want queries/ai.sql", e.Queries)
	}
	if e.Schema != "migrations" {
		t.Errorf("schema = %q, want migrations", e.Schema)
	}
	if e.Out != "internal/store/postgres/aigen" {
		t.Errorf("out = %q, want internal/store/postgres/aigen", e.Out)
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
