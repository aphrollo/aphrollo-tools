package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureWorkspacePackages/FilesPerPackage give "41 packages, about 10000
// files": aphrollo-tools benchmarks `ratchet check` at the scale of the
// monorepo it was built to dogfood on (borld's own CLAUDE.md counts 41
// crates), not an arbitrary round number.
const (
	fixtureWorkspacePackages    = 41
	fixtureWorkspaceFilesPerPkg = 244 // 41 * 244 = 10004
)

// benchLaw is a law with real per-line work: a regex-absent scan (the
// `.clamp(` shape borld's own nan-guard law uses) PLUS a line-count ceiling,
// so a cold scan pays for both a full read and a regex pass over every file
// in the fixture, not just a stat.
const benchLaw = `
name = "bench-guard"
description = "benchmark fixture: no bare TODO"
severity = "warn"
baseline = ".ratchet/baselines/bench-guard.txt"

[scope]
include = ["**/*.go"]
exclude = ["**/vendor/**"]

[matcher]
kind = "regex-absent"
pattern = "TODO_NEVER_PRESENT"
key = "file:line-content-hash"
`

// buildFixtureWorkspace writes fixtureWorkspacePackages packages of
// fixtureWorkspaceFilesPerPkg small .go files each, plus the one law above,
// under root. It is called once per benchmark (outside the timed loop) —
// generating ~10000 files is itself real disk I/O the benchmark must not
// charge to the thing it measures.
func buildFixtureWorkspace(b *testing.B, root string) {
	b.Helper()
	lawDir := filepath.Join(root, ".ratchet", "laws")
	if err := os.MkdirAll(lawDir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lawDir, "bench-guard.toml"), []byte(benchLaw), 0o644); err != nil {
		b.Fatal(err)
	}
	for p := 0; p < fixtureWorkspacePackages; p++ {
		pkgDir := filepath.Join(root, fmt.Sprintf("pkg%03d", p))
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < fixtureWorkspaceFilesPerPkg; f++ {
			content := fmt.Sprintf("package pkg%03d\n\n// file %d of the fixture workspace.\nfunc F%d() int {\n\treturn %d\n}\n", p, f, f, f)
			path := filepath.Join(pkgDir, fmt.Sprintf("file%04d.go", f))
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkRatchetCheckCold runs `ratchet check` with caching disabled
// (CacheDir ""), the shape a first run on a box — or a run whose cache
// invalidated because the law set changed — actually pays: every file read
// and regex-scanned from scratch, every iteration.
func BenchmarkRatchetCheckCold(b *testing.B) {
	root := b.TempDir()
	buildFixtureWorkspace(b, root)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Check(Options{Root: root}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRatchetCheckWarm runs the SAME scan with a persistent CacheDir
// primed by one untimed warm-up call, the shape the gate hits on every edit
// after the first: unchanged files answer from their recorded hits instead
// of being read and re-scanned.
func BenchmarkRatchetCheckWarm(b *testing.B) {
	root := b.TempDir()
	buildFixtureWorkspace(b, root)
	cacheDir := b.TempDir()
	if _, err := Check(Options{Root: root, CacheDir: cacheDir}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Check(Options{Root: root, CacheDir: cacheDir}); err != nil {
			b.Fatal(err)
		}
	}
}

// manyCodeOnlyLawsCount is "a dozen-plus laws over a typical .rs file", the
// scale BorldsHitsIn's own doc comment names as the cost a per-law re-split
// pays on every cold scan of a single edited file.
const manyCodeOnlyLawsCount = 15

// buildManyCodeOnlyLawsFixture writes manyCodeOnlyLawsCount CodeOnly
// regex-absent laws, all scoped to the same files, plus a small workspace —
// small, because this benchmark's whole point is the PER-LAW work scanTree
// repeats on each file, not the file count BenchmarkRatchetCheckCold already
// covers.
func buildManyCodeOnlyLawsFixture(b *testing.B, root string) {
	b.Helper()
	lawDir := filepath.Join(root, ".ratchet", "laws")
	if err := os.MkdirAll(lawDir, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < manyCodeOnlyLawsCount; i++ {
		law := fmt.Sprintf(`
name = "bench-guard-%02d"
description = "benchmark fixture: no bare TODO_NEVER_PRESENT_%02d"
severity = "warn"
baseline = ".ratchet/baselines/bench-guard-%02d.txt"
code_only = true

[scope]
include = ["**/*.go"]
exclude = ["**/vendor/**"]

[matcher]
kind = "regex-absent"
pattern = "TODO_NEVER_PRESENT_%02d"
key = "file:line-content-hash"
`, i, i, i, i)
		path := filepath.Join(lawDir, fmt.Sprintf("bench-guard-%02d.toml", i))
		if err := os.WriteFile(path, []byte(law), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	const packages, filesPerPkg, linesPerFile = 4, 50, 300 // 300: a module_size-target-sized file, not a 5-line stub
	for p := 0; p < packages; p++ {
		pkgDir := filepath.Join(root, fmt.Sprintf("pkg%03d", p))
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < filesPerPkg; f++ {
			var sb strings.Builder
			fmt.Fprintf(&sb, "package pkg%03d\n\n", p)
			for ln := 0; ln < linesPerFile; ln++ {
				fmt.Fprintf(&sb, "func F%d_%d() int { return %d } // line %d of the fixture body\n", f, ln, ln, ln)
			}
			path := filepath.Join(pkgDir, fmt.Sprintf("file%04d.go", f))
			if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkRatchetCheckColdManyCodeOnlyLaws times a cold scan where
// manyCodeOnlyLawsCount CodeOnly laws all apply to every file, the shape
// that pays scanTree's per-(law,file) splitLines/splitTrailingComment cost
// once per applicable law instead of once per file.
func BenchmarkRatchetCheckColdManyCodeOnlyLaws(b *testing.B) {
	root := b.TempDir()
	buildManyCodeOnlyLawsFixture(b, root)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Check(Options{Root: root}); err != nil {
			b.Fatal(err)
		}
	}
}
