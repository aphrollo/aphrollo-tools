package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// One law needs every in-scope file's RAW CONTENT (registry-both-ways reads
// the whole tree to answer "does anything use this entry"), and that turned
// the mtime cache off for the WHOLE run: every file was re-read and every
// law's matcher re-ran on it, every time. Measured in borld, 26 laws over
// 1936 files: 5.6 s on a warm cache, against 99 ms for the same law's scan
// when it ran alone and the cache served it.

// contentLawTree is a repo with a content-hungry law scoped to crates/**, a
// plain regex law scoped to docs/**, and one file in each.
func contentLawTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "env-registry", `
name = "env-registry"
description = "every switch is registered"
severity = "deny"
baseline = ".ratchet/baselines/env-registry.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "registry-both-ways"
registry_file = ".ratchet/registry/env.txt"
entry_pattern = "^([A-Z][A-Z0-9_]+) \|"
use_pattern = "env::var\(\"([A-Z][A-Z0-9_]+)\"\)"
`)
	writeLaw(t, root, "no-todo", `
name = "no-todo"
description = "prose carries no TODO"
severity = "deny"
baseline = ".ratchet/baselines/no-todo.txt"

[scope]
include = ["docs/**/*.md"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	write(t, filepath.Join(root, ".ratchet", "registry", "env.txt"), "BORLD_KNOWN | client | does a thing\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = env::var(\"BORLD_KNOWN\");\n")
	write(t, filepath.Join(root, "docs", "book", "vision.md"), "# vision\n\nnothing to do here\n")
	return root
}

func TestCheckCacheServesTheLawsThatDoNotNeedContent(t *testing.T) {
	root := contentLawTree(t)
	cache := t.TempDir()
	if _, err := Check(Options{Root: root, CacheDir: cache}); err != nil {
		t.Fatal(err)
	}
	second, err := Check(Options{Root: root, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if second.FilesMatched != 0 {
		t.Errorf("a repeat run re-matched %d file(s) — one content-hungry law must not turn the cache off for every other law", second.FilesMatched)
	}
	if second.FilesRead != 1 {
		t.Errorf("files read = %d, want only the one file the content-hungry law is scoped to", second.FilesRead)
	}
	if len(second.Findings) != 0 {
		t.Errorf("a cached run must reach the same verdict: %+v", second.Findings)
	}
}

// The content-hungry law still answers BOTH directions from the cached run:
// its whole point is a whole-tree question, and serving hits from a cache must
// not turn a stale registry entry invisible.
func TestCheckCacheKeepsTheContentLawAnsweringBothWays(t *testing.T) {
	root := contentLawTree(t)
	cache := t.TempDir()
	if _, err := Check(Options{Root: root, CacheDir: cache}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".ratchet", "registry", "env.txt"),
		"BORLD_KNOWN | client | does a thing\nBORLD_GONE | client | nothing reads it\n")

	res, err := Check(Options{Root: root, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(res.Lines(), "\n"); !strings.Contains(joined, "BORLD_GONE") {
		t.Fatalf("a stale entry must still be found on a cached run:\n%s", joined)
	}
}
