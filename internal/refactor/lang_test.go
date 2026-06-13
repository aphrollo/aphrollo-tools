package refactor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"a.go":  "gopls",
		"a.rs":  "rust-analyzer",
		"a.py":  "pyright-langserver",
		"a.ts":  "typescript-language-server",
		"a.tsx": "typescript-language-server",
	}
	for file, wantCmd := range cases {
		lang, err := DetectLanguage(file)
		if err != nil {
			t.Fatalf("DetectLanguage(%q): %v", file, err)
		}
		if lang.Command != wantCmd {
			t.Fatalf("DetectLanguage(%q).Command = %q, want %q", file, lang.Command, wantCmd)
		}
	}

	if _, err := DetectLanguage("a.cobol"); err == nil {
		t.Fatalf("DetectLanguage of unsupported extension: want error, got nil")
	}
}

func TestFindProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindProjectRoot(nested, []string{"go.mod"})
	if err != nil {
		t.Fatalf("FindProjectRoot: %v", err)
	}
	// macOS tmp dirs are symlinked; compare resolved paths.
	gotResolved, _ := filepath.EvalSymlinks(got)
	rootResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != rootResolved {
		t.Fatalf("FindProjectRoot = %q, want %q", gotResolved, rootResolved)
	}

	if _, err := FindProjectRoot(nested, []string{"nonexistent.marker"}); err == nil {
		t.Fatalf("FindProjectRoot with no marker: want error, got nil")
	}
}
