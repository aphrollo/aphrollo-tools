package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// preEditFixtureLaw gives RatchetAdvisory real per-edit work: one deny law
// scanning every proposed .go file, so the benchmark measures the whole
// pre-edit path (git rev-parse for the repo root, HasLaws, the scoped
// ratchet.Check, editRegressions) rather than an early "no laws here" exit.
const preEditFixtureLaw = `
name = "bench-guard"
description = "benchmark fixture: no bare TODO"
severity = "warn"
baseline = ".ratchet/baselines/bench-guard.txt"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO_NEVER_PRESENT"
key = "file:line-content-hash"
`

// buildPreEditFixtureWorkspace is check_bench_test.go's ratchet fixture
// shape, reused here so both benchmarks measure the SAME "41 packages,
// ~10000 files" scale — but rooted in a real git repo (copied from the
// package's own initFixture, built once in TestMain) rather than a bare
// directory: RatchetAdvisory's first real cost is `git rev-parse
// --show-toplevel` to find the repo root at all, and only a real .git makes
// that call succeed.
func buildPreEditFixtureWorkspace(b *testing.B, root string) {
	b.Helper()
	mustCopyDir(root, initFixture)
	lawDir := filepath.Join(root, ".ratchet", "laws")
	if err := os.MkdirAll(lawDir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lawDir, "bench-guard.toml"), []byte(preEditFixtureLaw), 0o644); err != nil {
		b.Fatal(err)
	}
	const packages, filesPerPkg = 41, 244 // 41 * 244 = 10004, matches internal/ratchet's fixture scale
	for p := 0; p < packages; p++ {
		pkgDir := filepath.Join(root, fmt.Sprintf("pkg%03d", p))
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < filesPerPkg; f++ {
			content := fmt.Sprintf("package pkg%03d\n\nfunc F%d() int {\n\treturn %d\n}\n", p, f, f)
			path := filepath.Join(pkgDir, fmt.Sprintf("file%04d.go", f))
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// preEditWritePayload is a Write call landing on one file deep in the
// fixture — the shape RatchetAdvisory actually receives from the PreToolUse
// hook — with fresh content each call so the file's mtime/size genuinely
// change and the scoped Check cannot silently answer from a cache the
// benchmark never asked for.
func preEditWritePayload(b *testing.B, path string, n int) []byte {
	b.Helper()
	payload := map[string]any{
		"tool_name": "Write",
		"tool_input": map[string]any{
			"file_path": path,
			"content":   fmt.Sprintf("package pkg000\n\nfunc F0() int {\n\treturn %d\n}\n", n),
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		b.Fatal(err)
	}
	return raw
}

// BenchmarkPreEditJudge times RatchetAdvisory — the edit-time law gate a
// Write/Edit/MultiEdit hits before it lands — against a single file inside a
// 41-package, ~10000-file repo, so a future change that makes it walk more
// than the one file it needs shows up as a regression here rather than only
// as a slow session.
func BenchmarkPreEditJudge(b *testing.B) {
	root := b.TempDir()
	buildPreEditFixtureWorkspace(b, root)
	target := filepath.Join(root, "pkg000", "file0000.go")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RatchetAdvisory(preEditWritePayload(b, target, i))
	}
}
