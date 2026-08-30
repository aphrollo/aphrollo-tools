package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestLooksLikeRepoPath(t *testing.T) {
	tests := []struct {
		name string
		tok  string
		want bool
	}{
		// Form A: contains a slash and the last segment has an extension.
		{"nested file with ext", "internal/cli/cli.go", true},
		{"dotdir nested file", ".github/workflows/pipeline.yml", true},
		{"two segment file", "deploy/deploy-prod.sh", true},
		// Form B: multi-segment directory (trailing slash + interior slash).
		{"nested dir", "internal/cli/", true},
		{"dotdir nested dir", ".github/workflows/", true},

		// Not paths: no slash at all.
		{"bare filename", "README.md", false},
		{"bare word", "aphrollo", false},
		// Bare single-segment directory is a concept, not a citation.
		{"bare dir", "node_modules/", false},
		{"bare dir migrations", "migrations/", false},
		// Slash but last segment has no extension → not a file citation.
		{"module path", "github.com/aphrollo/aphrollo-tools", false},
		{"api verb", "textDocument/documentSymbol", false},
		{"git ref", "origin/HEAD", false},
		// Commands / prose contain whitespace.
		{"command", "go build -o aphrollo ./cmd/aphrollo", false},
		{"git diff cmd", "git diff origin/main...HEAD", false},
		// Placeholders are not concrete paths.
		{"angle placeholder", "a/<path>", false},
		{"ellipsis placeholder", ".worktrees/…", false},
		{"glob", "queries/*.sql", false},
		{"pipe options", "/tdd [status|off|on]", false},
		// Brace placeholders (template substitution) are not concrete paths.
		{"brace placeholder", "references/{your_language}/determinism.md", false},
		{"brace list placeholder", "messages/{en,de,fr}.json", false},
		{"brace dir placeholder", "src/routes/p/{slug}/", false},
		// Absolute and home paths are not repo-relative citations.
		{"absolute path", "/usr/local/bin/aphrollo", false},
		{"absolute file", "/etc/foo/bar.yml", false},
		{"home path", "~/spaces/x", false},
		{"home file", "~/foo/bar.md", false},
		// Empty.
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeRepoPath(tt.tok); got != tt.want {
				t.Errorf("looksLikeRepoPath(%q) = %v, want %v", tt.tok, got, tt.want)
			}
		})
	}
}

func refsEqual(a, b []reference) bool {
	sort.Slice(a, func(i, j int) bool {
		if a[i].Line != a[j].Line {
			return a[i].Line < a[j].Line
		}
		return a[i].Path < a[j].Path
	})
	sort.Slice(b, func(i, j int) bool {
		if b[i].Line != b[j].Line {
			return b[i].Line < b[j].Line
		}
		return b[i].Path < b[j].Path
	})
	return reflect.DeepEqual(a, b)
}

func TestExtractRefs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []reference
	}{
		{
			name:    "markdown link to relative path",
			content: "see [the plan](plan.md) for details\n",
			want:    []reference{{Line: 1, Path: "plan.md"}},
		},
		{
			name:    "markdown link strips fragment",
			content: "[section](docs/crud-contract.md#section-7)\n",
			want:    []reference{{Line: 1, Path: "docs/crud-contract.md"}},
		},
		{
			name:    "markdown link strips title",
			content: "[x](path/to/file.go \"a title\")\n",
			want:    []reference{{Line: 1, Path: "path/to/file.go"}},
		},
		{
			name:    "image link target",
			content: "![alt](assets/diagram.png)\n",
			want:    []reference{{Line: 1, Path: "assets/diagram.png"}},
		},
		{
			name:    "inline code path with extension",
			content: "edit `internal/cli/cli.go` to change it\n",
			want:    []reference{{Line: 1, Path: "internal/cli/cli.go"}},
		},
		{
			name:    "inline code nested dir",
			content: "generated into `internal/lsp/` there\n",
			want:    []reference{{Line: 1, Path: "internal/lsp/"}},
		},
		{
			name:    "multiple refs on one line",
			content: "`a/b.go` and [c](d/e.md) here\n",
			want:    []reference{{Line: 1, Path: "a/b.go"}, {Line: 1, Path: "d/e.md"}},
		},
		{
			name:    "line numbers tracked",
			content: "line one\n`x/y.go`\nline three\n[z](p/q.md)\n",
			want:    []reference{{Line: 2, Path: "x/y.go"}, {Line: 4, Path: "p/q.md"}},
		},
		// A `:line` citation suffix is a location within a file, not part of
		// the path — strip it so the file itself is what gets resolved.
		{
			name:    "inline code strips single line suffix",
			content: "see `internal/ticketflow/workflow.go:288` here\n",
			want:    []reference{{Line: 1, Path: "internal/ticketflow/workflow.go"}},
		},
		{
			name:    "inline code strips line range suffix",
			content: "see `internal/store/postgres/threads.go:46-52` here\n",
			want:    []reference{{Line: 1, Path: "internal/store/postgres/threads.go"}},
		},
		{
			name:    "inline code strips line list suffix",
			content: "see `cmd/api/main.go:413,458,515` here\n",
			want:    []reference{{Line: 1, Path: "cmd/api/main.go"}},
		},
		{
			name:    "non-numeric colon suffix is not a line ref",
			content: "image `apache/tika:3.3.0.0-full` pinned\n",
			want:    []reference{{Line: 1, Path: "apache/tika:3.3.0.0-full"}},
		},

		// --- ignore cases ---
		{
			name:    "ignore http link",
			content: "[docs](https://example.com/a/b.html)\n",
			want:    nil,
		},
		{
			name:    "ignore http inline code",
			content: "`http://example.com/x/y.go`\n",
			want:    nil,
		},
		{
			name:    "ignore mailto link",
			content: "[mail](mailto:foo@example.com)\n",
			want:    nil,
		},
		{
			name:    "ignore bare anchor link",
			content: "[top](#introduction)\n",
			want:    nil,
		},
		{
			name:    "ignore non-path inline code",
			content: "run `go build` and `README.md` alone\n",
			want:    nil,
		},
		{
			name:    "ignore fenced code block",
			content: "before\n```\n`internal/cli/cli.go`\n[x](gone/missing.md)\n```\nafter `real/path.go`\n",
			want:    []reference{{Line: 6, Path: "real/path.go"}},
		},
		{
			name:    "ignore tilde fenced block",
			content: "~~~\n`skip/me.go`\n~~~\n`keep/me.go`\n",
			want:    []reference{{Line: 4, Path: "keep/me.go"}},
		},
		{
			name:    "ignore absolute and home inline",
			content: "`/usr/local/bin/aphrollo` and `~/CLAUDE.md`\n",
			want:    nil,
		},
		{
			name:    "link syntax quoted inside inline code is not a link",
			content: "targets like `[..](path)` are extracted\n",
			want:    nil,
		},
		{
			name:    "inline code masked but real link on same line kept",
			content: "quote `[x](y)` then link [real](a/b.md)\n",
			want:    []reference{{Line: 1, Path: "a/b.md"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRefs(tt.content)
			if !refsEqual(got, tt.want) {
				t.Errorf("extractRefs() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestCheckFiles is the falsifiable end-to-end case: a fixture doc that cites
// both a resolvable path and a deliberately dangling one; only the dangling
// reference must be reported.
func TestCheckFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Files the doc will cite.
	write("good.txt", "ok\n")
	write("sub/nested.md", "# nested\n")
	// A doc that cites: a sibling file (ok, relative to citing file),
	// a repo-root path (ok), and a dangling one (must be reported).
	write("docs/guide.md",
		"see `docs/../good.txt` and [nested](../sub/nested.md)\n"+ // both resolvable
			"but [gone](missing/removed.md) is dangling\n")

	got, err := CheckFiles(root, []string{"good.txt", "sub/nested.md", "docs/guide.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{{File: "docs/guide.md", Line: 2, Ref: "missing/removed.md"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckFiles() = %+v, want %+v", got, want)
	}
}

func TestResolveRelativeToCitingFileThenRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "sibling.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rooted.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	citing := "a/doc.md"
	if !resolves(root, citing, "sibling.md") {
		t.Errorf("expected sibling.md to resolve relative to citing file")
	}
	if !resolves(root, citing, "rooted.md") {
		t.Errorf("expected rooted.md to resolve relative to repo root")
	}
	if resolves(root, citing, "nope.md") {
		t.Errorf("expected nope.md to be unresolved")
	}
}
