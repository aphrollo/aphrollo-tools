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

// TestRename_TypeScript validates the typescript-language-server path end-to-end.
func TestRename_TypeScript(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH; skipping e2e")
	}

	dir := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"),
		[]byte("{\"compilerOptions\":{\"strict\":true},\"include\":[\"*.ts\"]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(dir, "a.ts")
	if err := os.WriteFile(mod,
		[]byte("export function greet(): string {\n  return \"hi\";\n}\n\nexport function caller(): string {\n  return greet();\n}\n"), 0o644); err != nil {
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
	if !strings.Contains(combined, "+export function hello(): string {") {
		t.Fatalf("ts rename missing declaration change:\n%s", combined)
	}
	if !strings.Contains(combined, "hello();") {
		t.Fatalf("ts rename missing call-site change:\n%s", combined)
	}
}
