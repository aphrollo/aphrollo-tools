package refactor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRename_Gopls_CrossFile is an end-to-end test against the real gopls
// binary: it renames a symbol used across two files and asserts both files'
// diffs. Skipped when gopls is not installed.
func TestRename_Gopls_CrossFile(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping e2e")
	}

	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("a.go", "package m\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n")
	write("b.go", "package m\n\nfunc Caller() string {\n\treturn Greet()\n}\n")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Rename(ctx, RenameRequest{
		File:    filepath.Join(dir, "a.go"),
		Line:    3,
		Symbol:  "Greet",
		NewName: "Hello",
	})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if len(res.Files) != 2 {
		t.Fatalf("rename touched %d files, want 2 (a.go + b.go)", len(res.Files))
	}

	byBase := map[string]string{}
	for _, f := range res.Files {
		byBase[filepath.Base(f.Path)] = f.Diff
	}
	if d := byBase["a.go"]; !strings.Contains(d, "+func Hello() string") {
		t.Fatalf("a.go diff missing declaration rename:\n%s", d)
	}
	if d := byBase["b.go"]; !strings.Contains(d, "+\treturn Hello()") {
		t.Fatalf("b.go diff missing reference rename:\n%s", d)
	}

	// Dry-run must not have touched disk.
	orig, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if strings.Contains(string(orig), "Hello") {
		t.Fatalf("dry-run rename modified a.go on disk")
	}
}

// TestFindReferences_Gopls finds a symbol's references across files.
func TestFindReferences_Gopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping e2e")
	}

	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("a.go", "package m\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n")
	write("b.go", "package m\n\nfunc Caller() string {\n\treturn Greet()\n}\n")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	refs, err := FindReferences(ctx, RefRequest{
		File:               filepath.Join(dir, "a.go"),
		Line:               3,
		Symbol:             "Greet",
		IncludeDeclaration: true,
	})
	if err != nil {
		t.Fatalf("FindReferences: %v", err)
	}
	if len(refs) < 2 {
		t.Fatalf("got %d references, want >= 2 (decl + use)", len(refs))
	}

	var sawUse bool
	for _, r := range refs {
		if filepath.Base(r.Path) == "b.go" && r.Line == 4 {
			sawUse = true
		}
	}
	if !sawUse {
		t.Fatalf("missing reference at b.go:4, got %+v", refs)
	}
}

// TestRename_Gopls_Apply verifies --apply writes the change to disk.
func TestRename_Gopls_Apply(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping e2e")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package m\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n"
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := Rename(ctx, RenameRequest{File: path, Line: 3, Symbol: "Greet", NewName: "Hello", Apply: true}); err != nil {
		t.Fatalf("Rename apply: %v", err)
	}

	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "func Hello() string") {
		t.Fatalf("apply did not rewrite file:\n%s", got)
	}
}
