package ratchet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A doc-path-resolves verdict is NOT a pure function of the citing file's
// bytes: it also depends on which oracle the run was given — the commit when
// it knows one (the commit and merge gates hand over `git ls-files`), the
// working tree when it does not (a bare `ratchet check`). The scan cache is
// keyed by the file's size and mtime and by the laws' text, and neither moves
// when the oracle changes. So a verdict reached under one oracle is served to
// a run under the other, and borld#301 comes straight back: one bare
// `aphrollo ratchet check` records "this citation is fine" from the disk, and
// the commit gate right after it is answered from that record instead of
// judging the commit.

const cacheOracleLaw = `name = "doc_reference_exists"
description = "Every cited path resolves."
severity = "deny"

[scope]
include = ["**/*.md"]

[matcher]
kind = "doc-path-resolves"
pattern = "(?:` + "`" + `|\]\()((?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+\.[A-Za-z0-9]+)"
`

// citingRepo lays out one repo whose NOTES.md cites docs/decisions.md, with
// the cited file present on disk and carried by no commit.
func citingRepo(t *testing.T) (root, cacheDir string) {
	t.Helper()
	root, cacheDir = t.TempDir(), t.TempDir()
	writeLaw(t, root, "doc_reference_exists", cacheOracleLaw)
	writeCited(t, root, "docs/decisions.md")
	if err := os.WriteFile(filepath.Join(root, "NOTES.md"), []byte("see `docs/decisions.md`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, cacheDir
}

// TestCheck_AWorktreeOracleVerdictIsNotServedToACommitOracleRun is the escape
// itself: the gate's run must judge the commit even when a disk-oracle run
// filled the cache for the same unchanged file moments earlier.
func TestCheck_AWorktreeOracleVerdictIsNotServedToACommitOracleRun(t *testing.T) {
	root, cacheDir := citingRepo(t)
	if _, err := Check(Options{Root: root, CacheDir: cacheDir}); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Root: root, CacheDir: cacheDir, Tracked: []string{"NOTES.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want one — docs/decisions.md is in no commit, whatever the previous run's disk said", res.Findings)
	}
}

// TestCheck_ACommitOracleVerdictIsNotServedToAWorktreeOracleRun is the same
// defect the other way round, and the one that costs a false refusal: a hit
// the gate recorded against the commit must not answer a bare `ratchet
// check`, whose documented oracle is the disk the file is sitting on.
func TestCheck_ACommitOracleVerdictIsNotServedToAWorktreeOracleRun(t *testing.T) {
	root, cacheDir := citingRepo(t)
	if _, err := Check(Options{Root: root, CacheDir: cacheDir, Tracked: []string{"NOTES.md"}}); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Root: root, CacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — with no tracked set the disk is the oracle and the file is on it", res.Findings)
	}
}

// TestCheck_ACachedCitationIsRejudgedWhenTheCommitDropsTheCitedFile is the
// same staleness without any oracle switch at all: two commit-oracle runs,
// the citing file untouched between them, the cited file gone from the index.
// A cache that records the VERDICT rather than the citation answers the
// second run from the first commit's contents.
func TestCheck_ACachedCitationIsRejudgedWhenTheCommitDropsTheCitedFile(t *testing.T) {
	root, cacheDir := citingRepo(t)
	res, err := Check(Options{Root: root, CacheDir: cacheDir, Tracked: []string{"NOTES.md", "docs/decisions.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — the first commit carries docs/decisions.md", res.Findings)
	}
	res, err = Check(Options{Root: root, CacheDir: cacheDir, Tracked: []string{"NOTES.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want one — this commit no longer carries docs/decisions.md", res.Findings)
	}
}

// TestCheck_ACitingFileIsStillServedFromTheCache is the cost side of the fix,
// and the reason it is not just "never cache a file a doc-path law claims".
// That law's scope is the whole tree in the repos that declare one, so
// dropping those entries would drop the cache; only the RESOLUTION is redone,
// and the file is neither re-read nor re-matched.
func TestCheck_ACitingFileIsStillServedFromTheCache(t *testing.T) {
	root, cacheDir := citingRepo(t)
	if _, err := Check(Options{Root: root, CacheDir: cacheDir, Tracked: []string{"NOTES.md"}}); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Root: root, CacheDir: cacheDir, Tracked: []string{"NOTES.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesMatched != 0 || res.FilesRead != 0 {
		t.Fatalf("second run read %d and matched %d file(s), want 0 and 0 — NOTES.md has not moved", res.FilesRead, res.FilesMatched)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want one — the citation is still unresolved, from the cached citation list", res.Findings)
	}
}

// TestCheck_RefusesACacheThatRecordedTheVerdictInsteadOfTheCitation closes the
// upgrade hole. An entry written by the previous format records whether the
// citation resolved and not what was cited, so the new code reads it as a
// file that cites nothing at all — the quietest possible way for this fix to
// ship and change nothing until every cache on every box happens to age out.
// The laws did not change, so only the cache version can say so.
func TestCheck_RefusesACacheThatRecordedTheVerdictInsteadOfTheCitation(t *testing.T) {
	const verdictOnly = 3
	root, cacheDir := citingRepo(t)
	laws, err := LoadLaws(root)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "NOTES.md"))
	if err != nil {
		t.Fatal(err)
	}
	stale := scanCache{
		Version: verdictOnly,
		Laws:    lawsFingerprint(laws),
		// What a disk-oracle run under the old format recorded: NOTES.md is
		// clean, and no trace of the citation that made it so.
		Files: map[string]cacheEntry{"NOTES.md": {Size: fi.Size(), Mtime: fi.ModTime().UnixNano()}},
	}
	body, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	path := loadCache(cacheDir, root, laws).path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Root: root, CacheDir: cacheDir, Tracked: []string{"NOTES.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want one — the recorded verdict predates the citation record and must not be served", res.Findings)
	}
}
