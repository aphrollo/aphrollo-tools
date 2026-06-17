package sqlc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These are e2e tests against a REAL `sqlc` binary, auto-skipped only when sqlc
// is genuinely not installed — the same "real tool or skip" convention the
// refactor package uses for its LSP server (see e2e_rust_test.go). We never fake
// sqlc; a fake would prove nothing about the regen/diff path that is the whole
// point of the command. The skip is routed through skipUnlessSQLC so the intent
// (tool-missing, not test-disabled) is explicit at every call site.

// skipUnlessSQLC skips the calling test when the sqlc binary is unavailable.
func skipUnlessSQLC(env *testing.T) {
	env.Helper()
	if sqlcBin() == "" {
		env.Skip("sqlc not installed (set APHROLLO_SQLC_BIN or add sqlc to PATH)")
	}
}

const widgetSchema = `CREATE TABLE widget (
  id   BIGINT PRIMARY KEY,
  name TEXT NOT NULL
);
`

const widgetQueries = `-- name: GetWidget :one
SELECT * FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT * FROM widget ORDER BY id;
`

const widgetConfig = `version: "2"
sql:
  - engine: "postgresql"
    queries: "queries.sql"
    schema: "schema.sql"
    gen:
      go:
        package: "gen"
        out: "gen"
`

// scaffoldRepo builds a minimal sqlc project and runs a real `sqlc generate` so
// the gen/ dir is "committed" output — the baseline a clean regen reproduces.
func scaffoldRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "sqlc.yaml"), widgetConfig)
	mustWrite(t, filepath.Join(repo, "schema.sql"), widgetSchema)
	mustWrite(t, filepath.Join(repo, "queries.sql"), widgetQueries)
	sqlcGen(t, repo)
	return repo
}

func sqlcGen(t *testing.T, repo string) {
	t.Helper()
	cmd := exec.Command(sqlcBin(), "-f", "sqlc.yaml", "generate")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlc generate: %v\n%s", err, out)
	}
}

func TestCheckCleanRepoReportsNoDrift(t *testing.T) {
	skipUnlessSQLC(t)
	repo := scaffoldRepo(t)
	cfgs, err := DiscoverConfigs(repo)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Check(cfgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 config result, got %d", len(res))
	}
	if len(res[0].Drifts) != 0 {
		t.Fatalf("clean repo should report no drift, got %d files", len(res[0].Drifts))
	}
	if AnyGatedDrift(res) {
		t.Errorf("clean repo must not flag gated drift")
	}
}

func TestCheckDetectsDrift(t *testing.T) {
	skipUnlessSQLC(t)
	repo := scaffoldRepo(t)
	genFile := filepath.Join(repo, "gen", "queries.sql.go")
	data, err := os.ReadFile(genFile)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, genFile, string(data)+"\n// stray hand edit\n")

	cfgs, _ := DiscoverConfigs(repo)
	res, err := Check(cfgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res[0].Drifts) == 0 {
		t.Fatal("expected drift after hand-editing a generated file")
	}
	if !AnyGatedDrift(res) {
		t.Errorf("a gated config with drift must flag gated drift (non-zero exit)")
	}
	if !strings.Contains(res[0].Drifts[0].Diff, "stray hand edit") {
		t.Errorf("drift diff should show the stray edit:\n%s", res[0].Drifts[0].Diff)
	}
}

func TestReportedOnlyConfigDoesNotFlagGatedDrift(t *testing.T) {
	skipUnlessSQLC(t)
	repo := scaffoldRepo(t)
	genFile := filepath.Join(repo, "gen", "queries.sql.go")
	data, _ := os.ReadFile(genFile)
	mustWrite(t, genFile, string(data)+"\n// post-edit\n")
	mustWrite(t, filepath.Join(repo, sidecarName), `configs:
  - file: sqlc.yaml
    clean: false
`)
	cfgs, _ := DiscoverConfigs(repo)
	res, err := Check(cfgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res[0].Drifts) == 0 {
		t.Fatal("drift should still be detected and reported")
	}
	if AnyGatedDrift(res) {
		t.Errorf("a reported-only config must NOT contribute to gated drift / non-zero exit")
	}
}
