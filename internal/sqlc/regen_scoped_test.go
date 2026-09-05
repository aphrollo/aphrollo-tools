package sqlc

import (
	"path/filepath"
	"slices"
	"testing"
)

// queryFiles must walk only the entry's own queries path(s), never the repo
// root: an entry whose Queries was silently dropped by the config parser (see
// TestParseConfig_ErrorsOnEmptyQueries) used to resolve to filepath.Join(repo,
// "") == repo, so queryFiles walked the ENTIRE repository for .sql files —
// misclassifying every pre-existing file outside the config's own scope as
// in-scope. This test scopes an entry to one subdirectory and asserts a .sql
// file elsewhere in the repo (including one at the repo root) never appears.
func TestQueryFiles_ScopesToTheEntrysOwnDirectoryNotTheRepoRoot(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "queries", "a.sql"), "-- name: A :one\nSELECT 1;\n")
	mustWrite(t, filepath.Join(repo, "outside.sql"), "-- name: Outside :one\nSELECT 1;\n")
	mustWrite(t, filepath.Join(repo, "other", "unrelated.sql"), "-- name: Unrelated :one\nSELECT 1;\n")

	cfg := Config{Repo: repo, Entries: []SQLEntry{{Queries: []string{"queries"}, Out: "out"}}}

	got, err := queryFiles(cfg)
	if err != nil {
		t.Fatalf("queryFiles: %v", err)
	}
	want := []string{"queries/a.sql"}
	if !slices.Equal(got, want) {
		t.Fatalf("queryFiles = %v, want %v — must not walk outside the entry's queries path", got, want)
	}
}

// The list form of `queries:` names more than one path in a single entry;
// queryFiles must collect .sql files from every one of them, still never
// straying outside the union of those paths.
func TestQueryFiles_CollectsEveryPathInAListFormEntry(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "a", "one.sql"), "-- name: One :one\nSELECT 1;\n")
	mustWrite(t, filepath.Join(repo, "b", "two.sql"), "-- name: Two :one\nSELECT 1;\n")
	mustWrite(t, filepath.Join(repo, "c", "three.sql"), "-- name: Three :one\nSELECT 1;\n")

	cfg := Config{Repo: repo, Entries: []SQLEntry{{Queries: []string{"a", "b"}, Out: "out"}}}

	got, err := queryFiles(cfg)
	if err != nil {
		t.Fatalf("queryFiles: %v", err)
	}
	want := []string{"a/one.sql", "b/two.sql"}
	if !slices.Equal(got, want) {
		t.Fatalf("queryFiles = %v, want %v", got, want)
	}
}

// An entry with no queries path is refused rather than silently resolving to
// the repo root (filepath.Join(repo, "") == repo).
func TestQueryFiles_RefusesAnEntryWithNoQueriesPath(t *testing.T) {
	repo := t.TempDir()
	cfg := Config{Repo: repo, Entries: []SQLEntry{{Out: "out"}}}

	if _, err := queryFiles(cfg); err == nil {
		t.Fatal("queryFiles with no queries path: want error, got nil")
	}
}

// A ".." in a config's queries value walks filepath.Join right back out of the
// repo it was joined against — `queries: "../secrets"` resolves to a sibling
// directory. queryFiles must refuse this rather than silently walking outside
// the repo it was invoked against.
func TestQueryFiles_RefusesAPathEscapingTheRepoRoot(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	mustWrite(t, filepath.Join(repo, "queries", "a.sql"), "-- name: A :one\nSELECT 1;\n")
	mustWrite(t, filepath.Join(parent, "secrets", "leak.sql"), "-- name: Leak :one\nSELECT 1;\n")

	cfg := Config{Repo: repo, Entries: []SQLEntry{{Queries: []string{"../secrets"}, Out: "out"}}}

	if _, err := queryFiles(cfg); err == nil {
		t.Fatal("queryFiles with a queries path escaping the repo root: want error, got nil")
	}
}
