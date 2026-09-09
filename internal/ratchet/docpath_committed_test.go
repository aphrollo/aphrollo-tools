package ratchet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A citation is a promise that the next reader can open the thing it names.
// The working tree is the wrong oracle for that promise: a file .gitignore
// excludes sits on the author's disk and in no other checkout, so a law that
// resolves against os.Stat calls the citation good and every clone finds
// nothing (borld#301 — crates/movement/docs/decisions.md matched .gitignore's
// *.md, `git add -A` skipped it, and four citations passed the gate). When
// the run knows what the commit contains, that set is the oracle.

// TestDocPathResolves_ACitationToAFileNoCommitCarriesIsAHit is the escape
// itself: the cited file exists on disk and is not in the commit.
func TestDocPathResolves_ACitationToAFileNoCommitCarriesIsAHit(t *testing.T) {
	dir := t.TempDir()
	writeCited(t, dir, "docs/decisions.md")
	l := docPathLaw(t, dir, genericDocPattern)
	l.Committed = map[string]bool{"NOTES.md": true}
	hits := l.HitsIn("NOTES.md", "see `docs/decisions.md`\n")
	if len(hits) != 1 || hits[0].What != "docs/decisions.md" {
		t.Fatalf("hits = %+v, want one hit for docs/decisions.md — it is in no commit", hits)
	}
}

// TestDocPathResolves_ACitationToACommittedFileIsClean is the other half: the
// law must go on accepting what the commit does carry, or it stops being a
// citation check and becomes a ban on citations.
func TestDocPathResolves_ACitationToACommittedFileIsClean(t *testing.T) {
	dir := t.TempDir()
	writeCited(t, dir, "docs/decisions.md")
	l := docPathLaw(t, dir, genericDocPattern)
	l.Committed = map[string]bool{"NOTES.md": true, "docs/decisions.md": true}
	if hits := l.HitsIn("NOTES.md", "see `docs/decisions.md`\n"); len(hits) != 0 {
		t.Fatalf("hits = %+v, want none — docs/decisions.md is in the commit", hits)
	}
}

// TestDocPathResolves_ACitationToADirectoryOfCommittedFilesIsClean proves the
// Form-B trailing-slash citation survives the switch: git tracks files, never
// directories, so a directory resolves through the files committed under it.
func TestDocPathResolves_ACitationToADirectoryOfCommittedFilesIsClean(t *testing.T) {
	dir := t.TempDir()
	l := docPathLaw(t, dir, genericDocPattern)
	l.Committed = CommittedPathSet([]string{"NOTES.md", "internal/tdd/gate.go"})
	if hits := l.HitsIn("NOTES.md", "see `internal/tdd/` for the gate\n"); len(hits) != 0 {
		t.Fatalf("hits = %+v, want none — internal/tdd/ holds a committed file", hits)
	}
}

// TestDocPathResolves_ADirectoryNoCommitCarriesIsAHit is that acceptance's
// floor: an ancestor of nothing committed resolves no better than a file.
func TestDocPathResolves_ADirectoryNoCommitCarriesIsAHit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "tdd"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := docPathLaw(t, dir, genericDocPattern)
	l.Committed = CommittedPathSet([]string{"NOTES.md"})
	hits := l.HitsIn("NOTES.md", "see `internal/tdd/` for the gate\n")
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one hit — internal/tdd/ is an empty dir no commit carries", hits)
	}
}

// TestDocPathResolves_AnUnknownCommitFallsBackToTheWorkingTree keeps the
// pre-edit hook and a bare `ratchet check` working: neither hands the checker
// a tracked set, and a rule that answered "nothing resolves" there would deny
// every citation in the repo.
func TestDocPathResolves_AnUnknownCommitFallsBackToTheWorkingTree(t *testing.T) {
	dir := t.TempDir()
	writeCited(t, dir, "docs/decisions.md")
	l := docPathLaw(t, dir, genericDocPattern)
	if hits := l.HitsIn("NOTES.md", "see `docs/decisions.md`\n"); len(hits) != 0 {
		t.Fatalf("hits = %+v, want none — with no commit set the disk is the only oracle", hits)
	}
}

// TestCheck_ResolvesCitationsAgainstTheTrackedSet proves the plumbing, not
// just the matcher: Options.Tracked is what the commit gate already passes,
// and it has to reach the law that resolves the citation.
func TestCheck_ResolvesCitationsAgainstTheTrackedSet(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "doc_reference_exists", `name = "doc_reference_exists"
description = "Every cited path resolves."
severity = "deny"

[scope]
include = ["**/*.md"]

[matcher]
kind = "doc-path-resolves"
pattern = "(?:^|\\s)((?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+\\.[A-Za-z0-9]+)"
`)
	writeCited(t, dir, "docs/decisions.md")
	if err := os.WriteFile(filepath.Join(dir, "NOTES.md"), []byte("see docs/decisions.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Root: dir, Tracked: []string{"NOTES.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want one — docs/decisions.md is untracked", res.Findings)
	}
}

// TestLoadCache_RefusesACacheFilledBeforeCitationsWereJudgedAgainstTheCommit
// closes the way this fix could ship and do nothing: a citing file nobody has
// touched is answered from its recorded hits, and those were reached when the
// working tree was the oracle. The cache version is what says the recorded
// verdicts were reached under a different rule.
func TestLoadCache_RefusesACacheFilledBeforeCitationsWereJudgedAgainstTheCommit(t *testing.T) {
	const beforeTheCommitOracle = 2
	dir, root := t.TempDir(), t.TempDir()
	laws := []Law{docPathLaw(t, root, genericDocPattern)}
	stale := scanCache{
		Version: beforeTheCommitOracle,
		Laws:    lawsFingerprint(laws),
		Files:   map[string]cacheEntry{"NOTES.md": {Size: 1, Mtime: 1}},
	}
	body, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	path := loadCache(dir, root, laws).path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadCache(dir, root, laws); len(got.Files) != 0 {
		t.Fatalf("cache served %d file(s) recorded under the working-tree oracle, want none", len(got.Files))
	}
}

// writeCited creates one repo-relative file with content the laws never read.
func writeCited(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
