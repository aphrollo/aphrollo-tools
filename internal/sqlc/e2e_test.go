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

const gadgetSchema = `CREATE TABLE widget (
  id   BIGINT PRIMARY KEY,
  name TEXT NOT NULL
);
CREATE TABLE gadget (
  id   BIGINT PRIMARY KEY,
  name TEXT NOT NULL
);
`

const gadgetQueries = `-- name: GetWidget :one
SELECT id, name FROM widget WHERE id = $1;

-- name: GetGadget :one
SELECT id, name FROM gadget WHERE id = $1;
`

func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRegenScopedAppliesOnlyChangedQuery is the acceptance scenario: a branch
// adds a param to ONE query while the schema has drifted (an unrelated column on
// another table). regen --scoped --apply must write only the changed query's
// hunks and leave the drift alone.
func TestRegenScopedAppliesOnlyChangedQuery(t *testing.T) {
	skipUnlessSQLC(t)
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "sqlc.yaml"), widgetConfig)
	mustWrite(t, filepath.Join(repo, "schema.sql"), gadgetSchema)
	mustWrite(t, filepath.Join(repo, "queries.sql"), gadgetQueries)
	sqlcGen(t, repo)
	git(t, repo, "init", "-q")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "base")

	// Pre-existing drift: add a column to gadget WITHOUT regenerating, so a clean
	// regen would change models.go (Gadget) + GetGadget — but GetGadget's query
	// text is untouched, so it is drift, not in scope.
	mustWrite(t, filepath.Join(repo, "schema.sql"), `CREATE TABLE widget (
  id   BIGINT PRIMARY KEY,
  name TEXT NOT NULL
);
CREATE TABLE gadget (
  id    BIGINT PRIMARY KEY,
  name  TEXT NOT NULL,
  label TEXT
);
`)
	// In-scope change: GetWidget gains a $2 name filter.
	mustWrite(t, filepath.Join(repo, "queries.sql"), `-- name: GetWidget :one
SELECT id, name FROM widget WHERE id = $1 AND name = $2;

-- name: GetGadget :one
SELECT id, name FROM gadget WHERE id = $1;
`)

	cfgs, err := DiscoverConfigs(repo)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RegenScoped(cfgs[0], "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ChangedQueries) != 1 || res.ChangedQueries[0] != "GetWidget" {
		t.Fatalf("changed queries = %v, want [GetWidget]", res.ChangedQueries)
	}
	if err := res.Apply(); err != nil {
		t.Fatal(err)
	}

	gen := readFile(t, filepath.Join(repo, "gen", "queries.sql.go"))
	models := readFile(t, filepath.Join(repo, "gen", "models.go"))

	// In-scope GetWidget hunk applied: the new param struct landed.
	if !strings.Contains(gen, "GetWidgetParams") {
		t.Errorf("GetWidget param change not applied:\n%s", gen)
	}
	// Drift NOT applied: Gadget.Label must be absent from models.go.
	if strings.Contains(models, "Label") {
		t.Errorf("drift (Gadget.Label) was applied to models.go — should be left alone:\n%s", models)
	}
	// And reported as drift, loudly.
	full := res.Render(true)
	if !strings.Contains(strings.ToLower(full), "drift") {
		t.Errorf("scoped render should report drift:\n%s", full)
	}
}
