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

// TestRename_Pyright validates the pyright-langserver path end-to-end.
func TestRename_Pyright(t *testing.T) {
	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		t.Skip("pyright-langserver not on PATH; skipping e2e")
	}

	dir := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[tool.pyright]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(dir, "a.py")
	if err := os.WriteFile(mod,
		[]byte("def greet():\n    return \"hi\"\n\n\ndef caller():\n    return greet()\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Rename(ctx, RenameRequest{File: mod, Line: 1, Symbol: "greet", NewName: "hello"})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	combined := ""
	for _, f := range res.Files {
		combined += f.Diff
	}
	if !strings.Contains(combined, "+def hello():") {
		t.Fatalf("python rename missing declaration change:\n%s", combined)
	}
	if !strings.Contains(combined, "hello()") {
		t.Fatalf("python rename missing call-site change:\n%s", combined)
	}
}
