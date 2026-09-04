package docs

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureDocsFileCount/LinesPerFile/CitesPerFile give "a realistic markdown
// tree": borld — the repo `ratchet check`'s own benchmark fixture cites for
// its dogfooding scale (internal/ratchet/check_bench_test.go) — carries 146
// tracked `*.md` files (measured via `git ls-files '*.md' | wc -l`,
// 2026-09-04). 30 lines/file with a citation every 6th line is a normal doc's
// prose-to-reference ratio, not an arbitrary round number.
const (
	fixtureDocsFileCount    = 146
	fixtureDocsLinesPerFile = 30
	fixtureDocsCitesPerFile = 5
)

// buildDocsFixtureRepo writes fixtureDocsFileCount markdown files under
// root/docs, each citing fixtureDocsCitesPerFile sibling .go files that it
// also creates, then commits the tree. Every citation resolves — the shape a
// passing PR's `docs check` actually walks — so docResolves pays its real
// stat cost instead of failing fast on the first lookup. Building the repo
// happens once outside the timed loop: the git spawns are real disk/process
// cost the benchmark must not charge to CheckFiles/Check themselves.
func buildDocsFixtureRepo(b *testing.B, root string) {
	b.Helper()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		b.Fatal(err)
	}
	runGit("init", "-q")
	runGit("config", "user.email", "bench@example.com")
	runGit("config", "user.name", "bench")

	citeEvery := fixtureDocsLinesPerFile / fixtureDocsCitesPerFile
	for i := 0; i < fixtureDocsFileCount; i++ {
		var sb strings.Builder
		fmt.Fprintf(&sb, "# doc %d\n\n", i)
		for l := 0; l < fixtureDocsLinesPerFile; l++ {
			if l%citeEvery == 0 {
				target := fmt.Sprintf("internal/pkg%03d/file%03d.go", i, l)
				fmt.Fprintf(&sb, "see `%s` for details.\n", target)
				goPath := filepath.Join(root, filepath.FromSlash(target))
				if err := os.MkdirAll(filepath.Dir(goPath), 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(goPath, []byte("package p\n"), 0o644); err != nil {
					b.Fatal(err)
				}
			} else {
				fmt.Fprintf(&sb, "line %d of prose with no citation at all.\n", l)
			}
		}
		mdPath := filepath.Join(root, "docs", fmt.Sprintf("doc%03d.md", i))
		if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(mdPath, []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "fixture")
}

// BenchmarkDocsCheck times the full `docs check` path — TrackedMarkdown's git
// ls-files walk plus CheckFiles's per-file law.HitsIn scan through the same
// ratchet engine `ratchet check` and the pre-edit judge both carry a
// benchmark for — over a 146-file markdown tree, so a regression in the
// shared matcher moves this number the same way it would move theirs.
func BenchmarkDocsCheck(b *testing.B) {
	root := b.TempDir()
	buildDocsFixtureRepo(b, root)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Check(root, nil, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}
