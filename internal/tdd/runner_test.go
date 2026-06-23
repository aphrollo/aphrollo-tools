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
	// Go test edit narrows to the package; source edit narrows to its package.
	goR := Runner{"go", []string{"test", "./..."}}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x_test.go", root); !reflect.DeepEqual(got, Runner{"go", []string{"test", "./internal/x/..."}}) {
		t.Fatalf("go test narrow = %+v", got)
	}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x.go", root); !reflect.DeepEqual(got, Runner{"go", []string{"test", "./internal/x"}}) {
		t.Fatalf("go source narrow = %+v", got)
	}
	// pytest test edit runs just that file.
	pyR := Runner{"pytest", []string{"-q"}}
	if got := NarrowToRelatedTests(pyR, "/proj/tests/test_a.py", root); !reflect.DeepEqual(got, Runner{"pytest", []string{"-q", "tests/test_a.py"}}) {
		t.Fatalf("pytest narrow = %+v", got)
	}
}

// TestNarrowToRelatedTests_SourceEdits covers the source-file analog of the
// test-file narrowing: a source edit runs only the tests whose import graph
// reaches the edited file, per runner. Unknown runners fall back to the broad
// suite.
func TestNarrowToRelatedTests_SourceEdits(t *testing.T) {
	root := "/proj"
	cases := []struct {
		name   string
		runner Runner
		target string
		want   Runner
	}{
		{
			name:   "vitest source → related --run",
			runner: Runner{"npx", []string{"vitest", "run"}},
			target: "/proj/src/widget.ts",
			want:   Runner{"npx", []string{"vitest", "related", "src/widget.ts", "--run"}},
		},
		{
			name:   "jest source → --findRelatedTests",
			runner: Runner{"npx", []string{"jest"}},
			target: "/proj/src/widget.js",
			want:   Runner{"npx", []string{"jest", "--findRelatedTests", "src/widget.js"}},
		},
		{
			name:   "go source → package dir",
			runner: Runner{"go", []string{"test", "./..."}},
			target: "/proj/internal/x/x.go",
			want:   Runner{"go", []string{"test", "./internal/x"}},
		},
		{
			name:   "unknown js script source → full-suite fallback",
			runner: Runner{"npm", []string{"test", "--silent"}},
			target: "/proj/src/widget.ts",
			want:   Runner{"npm", []string{"test", "--silent"}},
		},
		{
			name:   "cargo source → full-suite fallback",
			runner: Runner{"cargo", []string{"test"}},
			target: "/proj/src/lib.rs",
			want:   Runner{"cargo", []string{"test"}},
		},
		{
			name:   "pytest source → full-suite fallback",
			runner: Runner{"pytest", []string{"-q"}},
			target: "/proj/pkg/widget.py",
			want:   Runner{"pytest", []string{"-q"}},
		},
		{
			name:   "outside-root source → broad command",
			runner: Runner{"go", []string{"test", "./..."}},
			target: "/elsewhere/x.go",
			want:   Runner{"go", []string{"test", "./..."}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NarrowToRelatedTests(c.runner, c.target, root); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("NarrowToRelatedTests = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestNarrowToRelatedTests_VitestVsJest pins the npx-runner branching to the
// detected runner name, not just "is it npx": vitest uses `related … --run`
// while jest uses `--findRelatedTests …`.
func TestNarrowToRelatedTests_VitestVsJest(t *testing.T) {
	root := "/proj"
	vitest := NarrowToRelatedTests(Runner{"npx", []string{"vitest", "run"}}, "/proj/a/b.ts", root)
	if !reflect.DeepEqual(vitest, Runner{"npx", []string{"vitest", "related", "a/b.ts", "--run"}}) {
		t.Fatalf("vitest source = %+v", vitest)
	}
	jest := NarrowToRelatedTests(Runner{"npx", []string{"jest"}}, "/proj/a/b.ts", root)
	if !reflect.DeepEqual(jest, Runner{"npx", []string{"jest", "--findRelatedTests", "a/b.ts"}}) {
		t.Fatalf("jest source = %+v", jest)
	}
}
