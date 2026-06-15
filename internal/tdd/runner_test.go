package tdd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mkProject creates a temp project root holding the given marker files and
// returns the root dir.
func mkProject(t *testing.T, markers ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, m := range markers {
		if err := os.WriteFile(filepath.Join(root, m), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindProjectRoot(t *testing.T) {
	root := mkProject(t, "go.mod")
	sub := filepath.Join(root, "internal", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got := FindProjectRoot(filepath.Join(sub, "x.go"))
	if got != root {
		t.Fatalf("FindProjectRoot = %q, want %q", got, root)
	}

	if got := FindProjectRoot(filepath.Join(t.TempDir(), "loose.go")); got != "" {
		t.Fatalf("expected no root for a marker-less tree, got %q", got)
	}
}

func TestDetectRunner(t *testing.T) {
	cases := []struct {
		marker string
		want   Runner
	}{
		{"go.mod", Runner{"go", []string{"test", "./..."}}},
		{"Cargo.toml", Runner{"cargo", []string{"test"}}},
		{"pyproject.toml", Runner{"pytest", []string{"-q"}}},
	}
	for _, c := range cases {
		root := mkProject(t, c.marker)
		got, ok := DetectRunner(root)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Fatalf("DetectRunner(%s) = %+v,%v want %+v", c.marker, got, ok, c.want)
		}
	}

	if _, ok := DetectRunner(t.TempDir()); ok {
		t.Fatal("expected no runner for an unmarked project")
	}
}

func TestDetectRunner_Vitest(t *testing.T) {
	root := t.TempDir()
	pkg := `{"devDependencies":{"vitest":"^1.0.0"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := DetectRunner(root)
	want := Runner{"npx", []string{"vitest", "run"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectRunner vitest = %+v, want %+v", got, want)
	}
}

func TestNarrowToRelatedTests(t *testing.T) {
	root := "/proj"
	// Go test edit narrows to the package; source edit stays broad.
	goR := Runner{"go", []string{"test", "./..."}}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x_test.go", root); !reflect.DeepEqual(got, Runner{"go", []string{"test", "./internal/x/..."}}) {
		t.Fatalf("go test narrow = %+v", got)
	}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x.go", root); !reflect.DeepEqual(got, goR) {
		t.Fatalf("source edit must stay broad, got %+v", got)
	}
	// pytest test edit runs just that file.
	pyR := Runner{"pytest", []string{"-q"}}
	if got := NarrowToRelatedTests(pyR, "/proj/tests/test_a.py", root); !reflect.DeepEqual(got, Runner{"pytest", []string{"-q", "tests/test_a.py"}}) {
		t.Fatalf("pytest narrow = %+v", got)
	}
}
