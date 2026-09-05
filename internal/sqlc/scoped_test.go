package sqlc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNameOf(t *testing.T) {
	cases := map[string]string{
		"func (q *Queries) GetWidget(ctx context.Context) error {": "GetWidget",
		"func GetWidget(ctx context.Context) error {":              "GetWidget",
		"type GetWidgetParams struct {":                            "GetWidgetParams",
		"type Widget struct {":                                     "Widget",
		"const getWidget = `SELECT 1`":                             "getWidget",
		"var foo = 1":                                              "foo",
		"\tnot a top-level decl":                                   "",
		"import \"context\"":                                       "",
		"package gen":                                              "",
	}
	for line, want := range cases {
		if got := nameOf(line); got != want {
			t.Errorf("nameOf(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestInScopeSet(t *testing.T) {
	set := inScopeSet([]string{"AppendStreamEvent"})
	for _, want := range []string{"AppendStreamEvent", "appendStreamEvent", "AppendStreamEventParams", "AppendStreamEventRow"} {
		if !set[want] {
			t.Errorf("in-scope set missing %q", want)
		}
	}
	if set["CrmTicket"] {
		t.Errorf("CrmTicket must not be in scope for an AppendStreamEvent change")
	}
}

func TestParseQueryBlocks(t *testing.T) {
	sql := `-- name: GetWidget :one
SELECT * FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT * FROM widget;
`
	blocks := parseQueryBlocks(sql)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	if !strings.Contains(blocks["GetWidget"], "WHERE id = $1") {
		t.Errorf("GetWidget block wrong: %q", blocks["GetWidget"])
	}
}

func TestChangedQueryNamesFromContent(t *testing.T) {
	base := `-- name: GetWidget :one
SELECT id, name FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT id, name FROM widget;
`
	work := `-- name: GetWidget :one
SELECT id, name, color FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT id, name FROM widget;
`
	changed := changedNamesBetween(base, work)
	if len(changed) != 1 || changed[0] != "GetWidget" {
		t.Errorf("want [GetWidget], got %v", changed)
	}
}

// scopedMerge keeps committed content, swapping in regen only for in-scope
// symbols (the changed query's func/const/Params/Row) and dropping drift.
func TestScopedMergeAppliesOnlyInScope(t *testing.T) {
	committed := `package gen

import "context"

const getWidget = ` + "`SELECT id, name FROM widget WHERE id = $1`" + `

func (q *Queries) GetWidget(ctx context.Context, id int64) (Widget, error) {
	return Widget{}, nil
}

func (q *Queries) ListWidgets(ctx context.Context) ([]Widget, error) {
	return nil, nil
}
`
	// Regen: GetWidget gained a column (in-scope), ListWidgets body churned
	// (drift — ListWidgets wasn't a changed query), and a brand-new drift type
	// CrmTicketType appeared (unrelated migration).
	regen := `package gen

import "context"

const getWidget = ` + "`SELECT id, name, color FROM widget WHERE id = $1`" + `

func (q *Queries) GetWidget(ctx context.Context, id int64) (Widget, error) {
	return Widget{Color: ""}, nil
}

func (q *Queries) ListWidgets(ctx context.Context) ([]Widget, error) {
	return []Widget{}, nil
}

type CrmTicketType struct {
	ID int64
}
`
	inScope := inScopeSet([]string{"GetWidget"})
	merged := scopedMerge(committed, regen, func(n string) bool { return inScope[n] })

	// In-scope GetWidget hunks applied.
	if !strings.Contains(merged, "name, color FROM widget") {
		t.Errorf("in-scope GetWidget const not applied:\n%s", merged)
	}
	if !strings.Contains(merged, "Widget{Color: \"\"}") {
		t.Errorf("in-scope GetWidget func body not applied:\n%s", merged)
	}
	// Drift backed out: ListWidgets stays at the committed body.
	if !strings.Contains(merged, "return nil, nil") {
		t.Errorf("ListWidgets drift should be backed out (kept committed):\n%s", merged)
	}
	if strings.Contains(merged, "return []Widget{}, nil") {
		t.Errorf("ListWidgets drift body leaked into merged output:\n%s", merged)
	}
	// Drift addition omitted entirely.
	if strings.Contains(merged, "CrmTicketType") {
		t.Errorf("drift-added type CrmTicketType must not be applied:\n%s", merged)
	}
}

func TestScopedMergeAppliesInScopeAddition(t *testing.T) {
	committed := `package gen

func (q *Queries) GetWidget() {}
`
	regen := `package gen

func (q *Queries) GetWidget() {}

func (q *Queries) CountWidgets() int { return 0 }
`
	inScope := inScopeSet([]string{"CountWidgets"})
	merged := scopedMerge(committed, regen, func(n string) bool { return inScope[n] })
	if !strings.Contains(merged, "CountWidgets") {
		t.Errorf("in-scope new query CountWidgets should be added:\n%s", merged)
	}
}

// gitShowTestRepo creates a minimal git repo with one committed file, returning
// its path.
func gitShowTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	mustWrite(t, repo+"/queries.sql", "-- name: GetWidget :one\nSELECT 1;\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "base")
	return repo
}

// TestGitShow_ReturnsEmptyForPathAbsentAtRef pins the one case gitShow may treat
// as "file was empty at base": a path that genuinely never existed at ref (a
// newly added query file), never an error.
func TestGitShow_ReturnsEmptyForPathAbsentAtRef(t *testing.T) {
	repo := gitShowTestRepo(t)
	got, err := gitShow(repo, "HEAD", "never-existed.sql")
	if err != nil {
		t.Fatalf("gitShow on an absent path must not error, got %v", err)
	}
	if got != "" {
		t.Errorf("gitShow on an absent path = %q, want empty", got)
	}
}

// TestGitShow_ReturnsErrorForUnresolvableRef pins the bug in #183: a bad/typo'd
// base ref must fail loud, never widen scope by being silently treated as an
// absent path (which would make changedNamesBetween see every query in the file
// as changed).
func TestGitShow_ReturnsErrorForUnresolvableRef(t *testing.T) {
	repo := gitShowTestRepo(t)
	_, err := gitShow(repo, "origin/does-not-exist-xyz", "queries.sql")
	if err == nil {
		t.Fatal("gitShow with an unresolvable ref must return an error, not \"\"")
	}
}

// TestGitShow_ReturnsEmptyForNewFileExistingOnDiskButAbsentAtRef pins the
// production case gitShow's own doc comment names: a newly added query file.
// The path exists on disk (changedQueries reads it with os.ReadFile right
// before calling gitShow) but was never committed, so HEAD's tree does not
// have it. Git's message for THIS shape of absence is worded differently from
// a path missing everywhere ("fatal: path '<rel>' exists on disk, but not in
// '<ref>'", verified against a real `git show` on git 2.53.0) — gitShow must
// still treat it as an absent-at-ref path, not an error, or `sqlc regen
// --scoped` hard-fails on the single most common reason to reach for it.
func TestGitShow_ReturnsEmptyForNewFileExistingOnDiskButAbsentAtRef(t *testing.T) {
	repo := gitShowTestRepo(t)
	mustWrite(t, repo+"/new-query.sql", "-- name: NewQuery :one\nSELECT 2;\n")

	got, err := gitShow(repo, "HEAD", "new-query.sql")
	if err != nil {
		t.Fatalf("gitShow on a path that exists on disk but not at ref must not error, got %v", err)
	}
	if got != "" {
		t.Errorf("gitShow on a path that exists on disk but not at ref = %q, want empty", got)
	}
}

// TestGitShow_ReturnsErrorWhenPathExistenceCheckFailsToStart pins the outer
// `if err != nil { return "", err }` right after the pathExistsAtRef call in
// gitShow. That branch guards a REAL execution failure of the `git cat-file
// -e` subprocess — as opposed to the object simply not existing, which
// pathExistsAtRef already converts to (false, nil). An OS-level argument
// length limit (Windows' ~32k command-line cap, Linux's ARG_MAX) reliably
// forces exactly that: the process never starts, so cmd.Run() returns a
// non-*exec.ExitError, and pathExistsAtRef propagates it as a real error.
// verifyRef, which runs first and does not carry relpath in its argv, still
// resolves HEAD fine, so this exercises gitShow's own propagation, not a
// misresolved ref.
func TestGitShow_ReturnsErrorWhenPathExistenceCheckFailsToStart(t *testing.T) {
	repo := gitShowTestRepo(t)
	hugeRelpath := strings.Repeat("a", 2_000_000) + ".sql"

	_, err := gitShow(repo, "HEAD", hugeRelpath)
	if err == nil {
		t.Fatal("gitShow must return an error when the existence check's git process fails to start, not \"\"")
	}
}

// gitShowFailsStubDir writes a fake `git` (a .cmd for Windows' PATHEXT lookup
// and an extensionless POSIX shell script for everywhere else, matching
// internal/tdd's fakeGitShim convention) that answers `rev-parse` and
// `cat-file` successfully but fails `show` — letting a test reach gitShow's
// second error branch (the actual `git show` command) without any real git
// history needing to produce that shape, which plain git commands cannot: a
// path that `cat-file -e` reports present must, in a healthy repo, also be
// readable by `show`.
func gitShowFailsStubDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := "@echo off\r\n" +
		"echo %* | findstr /C:\"show\" >nul\r\n" +
		"if not errorlevel 1 (\r\n" +
		"  echo fatal: stub show failure 1>&2\r\n" +
		"  exit /b 128\r\n" +
		")\r\n" +
		"echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(filepath.Join(dir, "git.cmd"), []byte(cmd), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *show*) echo \"fatal: stub show failure\" 1>&2; exit 128 ;;\n" +
		"  *) echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef; exit 0 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGitShow_ReturnsErrorWhenTheShowCommandFails pins gitShow's second `if err
// != nil { return "", err }`, for the `git show` command itself (as opposed to
// the existence check above it). Unlike the existence-check branch, this one
// does not filter by error shape — any failure of `git show`, including an
// ordinary non-zero exit, must propagate with the underlying message.
func TestGitShow_ReturnsErrorWhenTheShowCommandFails(t *testing.T) {
	repo := t.TempDir()
	dir := gitShowFailsStubDir(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := gitShow(repo, "HEAD", "queries.sql")
	if err == nil {
		t.Fatal("gitShow must return an error when the show command itself fails, not \"\"")
	}
	if !strings.Contains(err.Error(), "stub show failure") {
		t.Errorf("gitShow error = %v, want it to carry the underlying git show failure", err)
	}
}

// TestChangedQueries_WrapsAGitShowFailure pins regen_scoped.go's `if err != nil
// { return nil, fmt.Errorf(...) }` right after its gitShow call: a real gitShow
// failure (not an absent-at-ref path, which changedNamesBetween must still see
// as "everything in the working file is new") must abort changedQueries with a
// wrapped error, never be swallowed into an empty base and a widened diff.
func TestChangedQueries_WrapsAGitShowFailure(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "queries.sql"), "-- name: GetWidget :one\nSELECT 1;\n")
	cfg := Config{Repo: repo, Entries: []SQLEntry{{Queries: []string{"queries.sql"}}}}

	dir := gitShowFailsStubDir(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := changedQueries(cfg, "HEAD")
	if err == nil {
		t.Fatal("changedQueries must return an error when the underlying git show fails, not swallow it")
	}
	if !strings.Contains(err.Error(), "changed queries for") {
		t.Errorf("changedQueries error = %v, want it wrapped with the query-file context", err)
	}
}
