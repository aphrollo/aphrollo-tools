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

// TestZigRunner pins the zig project contract end to end: a build.zig (or
// build.zig.zon) root is found and runs `zig build test`, and because that
// command has no related-tests mode, every narrowing path leaves it unchanged
// — same full-suite fallback as cargo/pytest.
func TestZigRunner(t *testing.T) {
	root := mkProject(t, "build.zig")
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(filepath.Join(sub, "main.zig")); got != root {
		t.Fatalf("FindProjectRoot = %q, want %q", got, root)
	}

	zig := Runner{"zig", []string{"build", "test"}, ""}
	got, ok := DetectRunner(root)
	if !ok || !reflect.DeepEqual(got, zig) {
		t.Fatalf("DetectRunner = %+v,%v want %+v", got, ok, zig)
	}

	// narrowSourceEdit and narrowToStaged switch on the runner command; the zig
	// command matches no related-mode branch and must return unchanged.
	if got := narrowSourceEdit(zig, "src/main.zig", root); !reflect.DeepEqual(got, zig) {
		t.Fatalf("narrowSourceEdit = %+v, want %+v", got, zig)
	}
	if got, ok := narrowToStaged(zig, root, []string{"src/main.zig"}); ok || !reflect.DeepEqual(got, zig) {
		t.Fatalf("narrowToStaged = %+v,%v want %+v,false", got, ok, zig)
	}
}

func TestDetectRunner(t *testing.T) {
	cases := []struct {
		marker string
		want   Runner
	}{
		{"go.mod", Runner{"go", []string{"test", "./..."}, ""}},
		{"Cargo.toml", Runner{"cargo", []string{"test"}, ""}},
		{"pyproject.toml", Runner{"pytest", []string{"-q"}, ""}},
		{"build.zig", Runner{"zig", []string{"build", "test"}, ""}},
		{"build.zig.zon", Runner{"zig", []string{"build", "test"}, ""}},
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
	want := Runner{"npx", []string{"vitest", "run"}, ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectRunner vitest = %+v, want %+v", got, want)
	}
}

func TestNarrowToRelatedTests(t *testing.T) {
	root := "/proj"
	// Go test edit narrows to the package; source edit narrows to its package.
	goR := Runner{"go", []string{"test", "./..."}, ""}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x_test.go", root); !reflect.DeepEqual(got, Runner{"go", []string{"test", "./internal/x/..."}, ""}) {
		t.Fatalf("go test narrow = %+v", got)
	}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x.go", root); !reflect.DeepEqual(got, Runner{"go", []string{"test", "./internal/x"}, ""}) {
		t.Fatalf("go source narrow = %+v", got)
	}
	// pytest test edit runs just that file.
	pyR := Runner{"pytest", []string{"-q"}, ""}
	if got := NarrowToRelatedTests(pyR, "/proj/tests/test_a.py", root); !reflect.DeepEqual(got, Runner{"pytest", []string{"-q", "tests/test_a.py"}, ""}) {
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
			runner: Runner{"npx", []string{"vitest", "run"}, ""},
			target: "/proj/src/widget.ts",
			want:   Runner{"npx", []string{"vitest", "related", "src/widget.ts", "--run"}, ""},
		},
		{
			name:   "jest source → --findRelatedTests",
			runner: Runner{"npx", []string{"jest"}, ""},
			target: "/proj/src/widget.js",
			want:   Runner{"npx", []string{"jest", "--findRelatedTests", "src/widget.js"}, ""},
		},
		{
			name:   "go source → package dir",
			runner: Runner{"go", []string{"test", "./..."}, ""},
			target: "/proj/internal/x/x.go",
			want:   Runner{"go", []string{"test", "./internal/x"}, ""},
		},
		{
			name:   "unknown js script source → full-suite fallback",
			runner: Runner{"npm", []string{"test", "--silent"}, ""},
			target: "/proj/src/widget.ts",
			want:   Runner{"npm", []string{"test", "--silent"}, ""},
		},
		{
			name:   "cargo source → full-suite fallback",
			runner: Runner{"cargo", []string{"test"}, ""},
			target: "/proj/src/lib.rs",
			want:   Runner{"cargo", []string{"test"}, ""},
		},
		{
			name:   "pytest source → full-suite fallback",
			runner: Runner{"pytest", []string{"-q"}, ""},
			target: "/proj/pkg/widget.py",
			want:   Runner{"pytest", []string{"-q"}, ""},
		},
		{
			name:   "zig source → full-suite fallback",
			runner: Runner{"zig", []string{"build", "test"}, ""},
			target: "/proj/src/main.zig",
			want:   Runner{"zig", []string{"build", "test"}, ""},
		},
		{
			name:   "zig test → full-suite fallback",
			runner: Runner{"zig", []string{"build", "test"}, ""},
			target: "/proj/src/main_test.zig",
			want:   Runner{"zig", []string{"build", "test"}, ""},
		},
		{
			name:   "outside-root source → broad command",
			runner: Runner{"go", []string{"test", "./..."}, ""},
			target: "/elsewhere/x.go",
			want:   Runner{"go", []string{"test", "./..."}, ""},
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
	vitest := NarrowToRelatedTests(Runner{"npx", []string{"vitest", "run"}, ""}, "/proj/a/b.ts", root)
	if !reflect.DeepEqual(vitest, Runner{"npx", []string{"vitest", "related", "a/b.ts", "--run"}, ""}) {
		t.Fatalf("vitest source = %+v", vitest)
	}
	jest := NarrowToRelatedTests(Runner{"npx", []string{"jest"}, ""}, "/proj/a/b.ts", root)
	if !reflect.DeepEqual(jest, Runner{"npx", []string{"jest", "--findRelatedTests", "a/b.ts"}, ""}) {
		t.Fatalf("jest source = %+v", jest)
	}
}

// TestNarrowToStaged covers the precommit multi-file related-mode build: the
// union of staged files is scoped to one command per runner. Unknown runners
// (and runners with no related mode) fall back to the broad suite unchanged.
func TestNarrowToStaged(t *testing.T) {
	cases := []struct {
		name   string
		runner Runner
		files  []string
		want   Runner
		wantOK bool
	}{
		{
			name:   "vitest → related multi-file --run",
			runner: Runner{"npx", []string{"vitest", "run"}, ""},
			files:  []string{"src/a.ts", "src/a.test.ts", "src/b.ts"},
			want:   Runner{"npx", []string{"vitest", "related", "src/a.ts", "src/a.test.ts", "src/b.ts", "--run"}, ""},
			wantOK: true,
		},
		{
			name:   "vitest test-only staged → related on the test file --run",
			runner: Runner{"npx", []string{"vitest", "run"}, ""},
			files:  []string{"src/a.test.ts"},
			want:   Runner{"npx", []string{"vitest", "related", "src/a.test.ts", "--run"}, ""},
			wantOK: true,
		},
		{
			name:   "jest → --findRelatedTests multi-file",
			runner: Runner{"npx", []string{"jest"}, ""},
			files:  []string{"src/a.js", "src/b.js"},
			want:   Runner{"npx", []string{"jest", "--findRelatedTests", "src/a.js", "src/b.js"}, ""},
			wantOK: true,
		},
		{
			name:   "jest test-only staged → --findRelatedTests on the test file",
			runner: Runner{"npx", []string{"jest"}, ""},
			files:  []string{"src/a.test.js"},
			want:   Runner{"npx", []string{"jest", "--findRelatedTests", "src/a.test.js"}, ""},
			wantOK: true,
		},
		{
			name:   "go test-only staged → that package dir",
			runner: Runner{"go", []string{"test", "./..."}, ""},
			files:  []string{"internal/x/x_test.go"},
			want:   Runner{"go", []string{"test", "./internal/x"}, ""},
			wantOK: true,
		},
		{
			name:   "npx with empty args → full-suite fallback",
			runner: Runner{"npx", nil, ""},
			files:  []string{"src/a.ts"},
			want:   Runner{"npx", nil, ""},
			wantOK: false,
		},
		{
			name:   "go → deduped package dirs",
			runner: Runner{"go", []string{"test", "./..."}, ""},
			files:  []string{"internal/x/x.go", "internal/x/x_test.go", "internal/y/y.go"},
			want:   Runner{"go", []string{"test", "./internal/x", "./internal/y"}, ""},
			wantOK: true,
		},
		{
			name:   "go root-level file → dot package",
			runner: Runner{"go", []string{"test", "./..."}, ""},
			files:  []string{"main.go"},
			want:   Runner{"go", []string{"test", "."}, ""},
			wantOK: true,
		},
		{
			name:   "unknown js script → full-suite fallback",
			runner: Runner{"npm", []string{"test", "--silent"}, ""},
			files:  []string{"src/a.ts"},
			want:   Runner{"npm", []string{"test", "--silent"}, ""},
			wantOK: false,
		},
		{
			name:   "cargo → full-suite fallback",
			runner: Runner{"cargo", []string{"test"}, ""},
			files:  []string{"src/lib.rs"},
			want:   Runner{"cargo", []string{"test"}, ""},
			wantOK: false,
		},
		{
			name:   "pytest → full-suite fallback",
			runner: Runner{"pytest", []string{"-q"}, ""},
			files:  []string{"pkg/widget.py"},
			want:   Runner{"pytest", []string{"-q"}, ""},
			wantOK: false,
		},
		{
			name:   "zig → full-suite fallback",
			runner: Runner{"zig", []string{"build", "test"}, ""},
			files:  []string{"src/main.zig"},
			want:   Runner{"zig", []string{"build", "test"}, ""},
			wantOK: false,
		},
		{
			name:   "no files → full-suite fallback",
			runner: Runner{"go", []string{"test", "./..."}, ""},
			files:  nil,
			want:   Runner{"go", []string{"test", "./..."}, ""},
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := narrowToStaged(c.runner, t.TempDir(), c.files)
			if ok != c.wantOK || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("narrowToStaged = %+v,%v want %+v,%v", got, ok, c.want, c.wantOK)
			}
		})
	}
}
