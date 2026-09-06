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

const outlineSrc = "package m\n\n" +
	"func Greet() string {\n\treturn \"hi\"\n}\n\n" +
	"type Server struct {\n\tAddr string\n}\n"

func writeOutlineProject(t *testing.T) string {
	t.Helper()
	dir := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(outlineSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestOutline_Gopls maps a file's symbols against the real gopls binary.
func TestOutline_Gopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping e2e")
	}
	dir := writeOutlineProject(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	syms, err := Outline(ctx, filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	out := RenderOutline(syms)
	if !strings.Contains(out, "func Greet") {
		t.Fatalf("outline missing func Greet:\n%s", out)
	}
	if !strings.Contains(out, "struct Server") {
		t.Fatalf("outline missing struct Server:\n%s", out)
	}
}

// TestShow_Gopls prints one symbol's source against the real gopls binary.
func TestShow_Gopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping e2e")
	}
	dir := writeOutlineProject(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	src, err := Show(ctx, filepath.Join(dir, "a.go"), "Greet")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	want := "func Greet() string {\n\treturn \"hi\"\n}\n"
	if src != want {
		t.Fatalf("Show(Greet) =\n%q\nwant\n%q", src, want)
	}

	if _, err := Show(ctx, filepath.Join(dir, "a.go"), "Missing"); err == nil {
		t.Fatalf("Show(Missing): want not-found error, got nil")
	}
}
