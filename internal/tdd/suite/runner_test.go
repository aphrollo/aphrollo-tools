package suite

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func mkProject(t *testing.T, markers ...string) string {
	t.Helper()
	return tddtest.MkProject(t, markers...)
}

func TestFindProjectRoot(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	root := mkProject(t, "build.zig")
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(filepath.Join(sub, "main.zig")); got != root {
		t.Fatalf("FindProjectRoot = %q, want %q", got, root)
	}

	zig := Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}}
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
	t.Parallel()
	cases := []struct {
		marker string
		want   Runner
	}{
		{"go.mod", Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}}},
		{"Cargo.toml", Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}},
		{"pyproject.toml", Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "", Deadline: time.Time{}}},
		{"build.zig", Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}}},
		{"build.zig.zon", Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}}},
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
	t.Parallel()
	root := t.TempDir()
	pkg := `{"devDependencies":{"vitest":"^1.0.0"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := DetectRunner(root)
	want := Runner{Cmd: "npx", Args: []string{"vitest", "run"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectRunner vitest = %+v, want %+v", got, want)
	}
}

func TestNarrowToRelatedTests(t *testing.T) {
	t.Parallel()
	root := "/proj"
	// Go test edit narrows to the package; source edit narrows to its package.
	goR := Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x_test.go", root); !reflect.DeepEqual(got, Runner{Cmd: "go", Args: []string{"test", "./internal/x/..."}, Dir: "", Deadline: time.Time{}}) {
		t.Fatalf("go test narrow = %+v", got)
	}
	if got := NarrowToRelatedTests(goR, "/proj/internal/x/x.go", root); !reflect.DeepEqual(got, Runner{Cmd: "go", Args: []string{"test", "./internal/x"}, Dir: "", Deadline: time.Time{}}) {
		t.Fatalf("go source narrow = %+v", got)
	}
	// pytest test edit runs just that file.
	pyR := Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "", Deadline: time.Time{}}
	if got := NarrowToRelatedTests(pyR, "/proj/tests/test_a.py", root); !reflect.DeepEqual(got, Runner{Cmd: "pytest", Args: []string{"-q", "tests/test_a.py"}, Dir: "", Deadline: time.Time{}}) {
		t.Fatalf("pytest narrow = %+v", got)
	}
}

// TestNarrowToRelatedTests_SourceEdits covers the source-file analog of the
// test-file narrowing: a source edit runs only the tests whose import graph
// reaches the edited file, per runner. Unknown runners fall back to the broad
// suite.
func TestNarrowToRelatedTests_SourceEdits(t *testing.T) {
	t.Parallel()
	root := "/proj"
	cases := []struct {
		name   string
		runner Runner
		target string
		want   Runner
	}{
		{
			name:   "vitest source → related --run",
			runner: Runner{Cmd: "npx", Args: []string{"vitest", "run"}, Dir: "", Deadline: time.Time{}},
			target: "/proj/src/widget.ts",
			want:   Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/widget.ts", "--run"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "jest source → --findRelatedTests",
			runner: Runner{Cmd: "npx", Args: []string{"jest"}, Dir: "", Deadline: time.Time{}},
			target: "/proj/src/widget.js",
			want:   Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "src/widget.js"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "go source → package dir",
			runner: Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
			target: "/proj/internal/x/x.go",
			want:   Runner{Cmd: "go", Args: []string{"test", "./internal/x"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "unknown js script source → full-suite fallback",
			runner: Runner{Cmd: "npm", Args: []string{"test", "--silent"}, Dir: "", Deadline: time.Time{}},
			target: "/proj/src/widget.ts",
			want:   Runner{Cmd: "npm", Args: []string{"test", "--silent"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "cargo source → full-suite fallback",
			runner: Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}},
			target: "/proj/src/lib.rs",
			want:   Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "pytest source → full-suite fallback",
			runner: Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "", Deadline: time.Time{}},
			target: "/proj/pkg/widget.py",
			want:   Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "zig source → full-suite fallback",
			runner: Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}},
			target: "/proj/src/main.zig",
			want:   Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "zig test → full-suite fallback",
			runner: Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}},
			target: "/proj/src/main_test.zig",
			want:   Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "outside-root source → broad command",
			runner: Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
			target: "/elsewhere/x.go",
			want:   Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
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

// TestNarrowToRelatedTests_ConfigFileWithNoOwningPackage: editing aphrollo.toml
// alone used to narrow to `go test ./.`, and the repo root holds no .go files
// at all — the run failed outright with "no Go files ... [setup failed]" and
// that read as a plain RED for a file the edit never touched as code. A
// non-Go Source file goDataFileScope names is scoped to ITS declared reader;
// one it does not name, with no owning package either, falls back to the
// broad suite rather than a target `go test` cannot load.
func TestNarrowToRelatedTests_ConfigFileWithNoOwningPackage(t *testing.T) {
	t.Parallel()
	goR := Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}}

	t.Run("declared reader", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "aphrollo.toml", "[aphrollo]\n")
		write(t, root, "internal/tdd/mutation/x_test.go", "package mutation\n")

		got := NarrowToRelatedTests(goR, filepath.Join(root, "aphrollo.toml"), root)

		want := Runner{Cmd: "go", Args: []string{"test", "./internal/tdd/mutation"}, Dir: "", Deadline: time.Time{}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("NarrowToRelatedTests(aphrollo.toml) = %+v, want %+v", got, want)
		}
	})

	t.Run("no declared reader falls back to the broad suite", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "unmapped.toml", "[x]\n")

		got := NarrowToRelatedTests(goR, filepath.Join(root, "unmapped.toml"), root)

		if !reflect.DeepEqual(got, goR) {
			t.Fatalf("NarrowToRelatedTests(unmapped.toml) = %+v, want the broad runner %+v unchanged", got, goR)
		}
	})
}

// TestNarrowToRelatedTests_VitestVsJest pins the npx-runner branching to the
// detected runner name, not just "is it npx": vitest uses `related … --run`
// while jest uses `--findRelatedTests …`.
func TestNarrowToRelatedTests_VitestVsJest(t *testing.T) {
	t.Parallel()
	root := "/proj"
	vitest := NarrowToRelatedTests(Runner{Cmd: "npx", Args: []string{"vitest", "run"}, Dir: "", Deadline: time.Time{}}, "/proj/a/b.ts", root)
	if !reflect.DeepEqual(vitest, Runner{Cmd: "npx", Args: []string{"vitest", "related", "a/b.ts", "--run"}, Dir: "", Deadline: time.Time{}}) {
		t.Fatalf("vitest source = %+v", vitest)
	}
	jest := NarrowToRelatedTests(Runner{Cmd: "npx", Args: []string{"jest"}, Dir: "", Deadline: time.Time{}}, "/proj/a/b.ts", root)
	if !reflect.DeepEqual(jest, Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "a/b.ts"}, Dir: "", Deadline: time.Time{}}) {
		t.Fatalf("jest source = %+v", jest)
	}
}

// TestNarrowToStaged covers the precommit multi-file related-mode build: the
// union of staged files is scoped to one command per runner. Unknown runners
// (and runners with no related mode) fall back to the broad suite unchanged.
func TestNarrowToStaged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		runner Runner
		files  []string
		want   Runner
		wantOK bool
	}{
		{
			name:   "vitest → related multi-file --run",
			runner: Runner{Cmd: "npx", Args: []string{"vitest", "run"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/a.ts", "src/a.test.ts", "src/b.ts"},
			want:   Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/a.ts", "src/a.test.ts", "src/b.ts", "--run"}, Dir: "", Deadline: time.Time{}},
			wantOK: true,
		},
		{
			name:   "vitest test-only staged → related on the test file --run",
			runner: Runner{Cmd: "npx", Args: []string{"vitest", "run"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/a.test.ts"},
			want:   Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/a.test.ts", "--run"}, Dir: "", Deadline: time.Time{}},
			wantOK: true,
		},
		{
			name:   "jest → --findRelatedTests multi-file",
			runner: Runner{Cmd: "npx", Args: []string{"jest"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/a.js", "src/b.js"},
			want:   Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "src/a.js", "src/b.js"}, Dir: "", Deadline: time.Time{}},
			wantOK: true,
		},
		{
			name:   "jest test-only staged → --findRelatedTests on the test file",
			runner: Runner{Cmd: "npx", Args: []string{"jest"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/a.test.js"},
			want:   Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "src/a.test.js"}, Dir: "", Deadline: time.Time{}},
			wantOK: true,
		},
		{
			name:   "go test-only staged → that package dir",
			runner: Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
			files:  []string{"internal/x/x_test.go"},
			want:   Runner{Cmd: "go", Args: []string{"test", "./internal/x"}, Dir: "", Deadline: time.Time{}},
			wantOK: true,
		},
		{
			name:   "npx with empty args → full-suite fallback",
			runner: Runner{Cmd: "npx", Args: nil, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/a.ts"},
			want:   Runner{Cmd: "npx", Args: nil, Dir: "", Deadline: time.Time{}},
			wantOK: false,
		},
		{
			name:   "go → deduped package dirs",
			runner: Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
			files:  []string{"internal/x/x.go", "internal/x/x_test.go", "internal/y/y.go"},
			want:   Runner{Cmd: "go", Args: []string{"test", "./internal/x", "./internal/y"}, Dir: "", Deadline: time.Time{}},
			wantOK: true,
		},
		{
			name:   "go root-level file → dot package",
			runner: Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
			files:  []string{"main.go"},
			want:   Runner{Cmd: "go", Args: []string{"test", "."}, Dir: "", Deadline: time.Time{}},
			wantOK: true,
		},
		{
			name:   "unknown js script → full-suite fallback",
			runner: Runner{Cmd: "npm", Args: []string{"test", "--silent"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/a.ts"},
			want:   Runner{Cmd: "npm", Args: []string{"test", "--silent"}, Dir: "", Deadline: time.Time{}},
			wantOK: false,
		},
		{
			name:   "cargo → full-suite fallback",
			runner: Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/lib.rs"},
			want:   Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}},
			wantOK: false,
		},
		{
			name:   "pytest → full-suite fallback",
			runner: Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"pkg/widget.py"},
			want:   Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "", Deadline: time.Time{}},
			wantOK: false,
		},
		{
			name:   "zig → full-suite fallback",
			runner: Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}},
			files:  []string{"src/main.zig"},
			want:   Runner{Cmd: "zig", Args: []string{"build", "test"}, Dir: "", Deadline: time.Time{}},
			wantOK: false,
		},
		{
			name:   "no files → full-suite fallback",
			runner: Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
			files:  nil,
			want:   Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}},
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The staged files are CREATED, not just named: narrowing a Go
			// runner asks whether a directory actually holds a .go file,
			// because naming one that does not fails `go test` outright
			// rather than skipping it.
			root := t.TempDir()
			for _, f := range c.files {
				body := ""
				if strings.HasSuffix(f, ".go") {
					body = "package p\n"
				}
				write(t, root, f, body)
			}
			got, ok := narrowToStaged(c.runner, root, c.files)
			if ok != c.wantOK || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("narrowToStaged = %+v,%v want %+v,%v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

// narrowFailFirstTests scopes a cargo fail-first proof to exactly the staged
// tests: one owning package gets `-p` plus a sorted, deduped `--test` per
// integration file and `--lib` for an inline test module; an unconfirmed
// nested guess drops --test scoping for the package; several packages fall
// back to package granularity; a file no package owns never widens the run.
func TestNarrowFailFirstTests_CargoScopesToTheStagedTargets(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\n")
	write(t, root, "crates/beta/Cargo.toml", "[package]\nname = \"beta\"\n")
	restore := SetCargoTestTargetsForTest(func(string) map[string]map[string]bool {
		return map[string]map[string]bool{"alpha": {"soak": true}}
	})
	defer restore()

	cases := []struct {
		name   string
		runner []string
		tests  []string
		want   []string // nil = the runner comes back unchanged
	}{
		{"one integration file", []string{"test"}, []string{"crates/alpha/tests/wear.rs"},
			[]string{"test", "-p", "alpha", "--test", "wear"}},
		{"targets sorted and deduped, inline adds --lib", []string{"test"},
			[]string{"crates/alpha/tests/wear.rs", "crates/alpha/src/grip.rs", "crates/alpha/tests/axle.rs", "crates/alpha/tests/wear.rs"},
			[]string{"test", "-p", "alpha", "--test", "axle", "--test", "wear", "--lib"}},
		{"nextest verb kept", []string{"nextest", "run", "--no-fail-fast"}, []string{"crates/alpha/tests/wear.rs"},
			[]string{"nextest", "run", "-p", "alpha", "--test", "wear"}},
		{"confirmed nested target", []string{"test"}, []string{"crates/alpha/tests/soak/load.rs"},
			[]string{"test", "-p", "alpha", "--test", "soak"}},
		{"unconfirmed nested guess drops --test scoping", []string{"test"},
			[]string{"crates/alpha/tests/common/fixtures.rs", "crates/alpha/tests/wear.rs"},
			[]string{"test", "-p", "alpha"}},
		{"two packages fall back to package granularity", []string{"test"},
			[]string{"crates/beta/tests/b.rs", "crates/alpha/tests/a.rs"},
			[]string{"test", "-p", "alpha", "-p", "beta"}},
		{"unowned file is dropped from an owned run", []string{"test"},
			[]string{"tools/gen_test.rs", "crates/beta/tests/b.rs"},
			[]string{"test", "-p", "beta", "--test", "b"}},
		{"nothing owned leaves the runner alone", []string{"test"}, []string{"tools/gen_test.rs"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := Runner{Cmd: "cargo", Args: tc.runner}
			want := in
			if tc.want != nil {
				want = Runner{Cmd: "cargo", Args: tc.want}
			}

			got := narrowFailFirstTests(in, root, tc.tests)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("narrowFailFirstTests(%v) = %+v, want %+v", tc.tests, got, want)
			}
		})
	}
}

// A Go fail-first proof runs the staged test NAMES in their packages, under
// an anchored -run filter; TestMain is never named. Any staged file that
// declares no test, or sits outside any package, falls back rather than
// risking a filter that matches nothing.
func TestNarrowGoFailFirst_RunsTheStagedTestNamesInTheirPackages(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.21\n")
	write(t, root, "root_test.go", "package m\n\nfunc TestRoot(t *testing.T) {}\n")
	write(t, root, "internal/x/x.go", "package x\n")
	write(t, root, "internal/x/x_test.go", "package x\n\nfunc TestMain(m *testing.M) {}\nfunc TestWear(t *testing.T) {}\nfunc  TestGrip (t *testing.T) {}\nfunc BenchmarkWear(b *testing.B) {}\n")
	write(t, root, "internal/x/more_test.go", "package x\n\nfunc TestWear2(t *testing.T) {}\nfunc TestGrip(t *testing.T) {}\n")
	write(t, root, "internal/x/fuzz_test.go", "package x\n\nfunc FuzzWear(f *testing.F) {}\n")
	write(t, root, "assets/a_test.go.txt", "func TestAsset(t *testing.T) {}\n")

	cases := []struct {
		name   string
		tests  []string
		want   []string
		wantOK bool
	}{
		{"one file", []string{"internal/x/x_test.go"},
			[]string{"test", "./internal/x", "-run", "^(TestGrip|TestWear)$"}, true},
		{"two packages, names deduped and sorted", []string{"internal/x/more_test.go", "root_test.go", "internal/x/x_test.go"},
			[]string{"test", ".", "./internal/x", "-run", "^(TestGrip|TestRoot|TestWear|TestWear2)$"}, true},
		{"a file declaring no test", []string{"internal/x/x_test.go", "internal/x/fuzz_test.go"}, nil, false},
		{"an unreadable file", []string{"internal/x/gone_test.go"}, nil, false},
		{"nothing staged", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := Runner{Cmd: "go", Args: []string{"test", "./..."}}

			got, ok := narrowGoFailFirst(in, root, tc.tests)

			want := in
			if tc.wantOK {
				want = Runner{Cmd: "go", Args: tc.want}
			}
			if ok != tc.wantOK || !reflect.DeepEqual(got, want) {
				t.Fatalf("narrowGoFailFirst(%v) = (%+v, %v), want (%+v, %v)", tc.tests, got, ok, want, tc.wantOK)
			}
		})
	}
	if _, ok := narrowGoFailFirst(Runner{Cmd: "cargo", Args: []string{"test"}}, root, []string{"internal/x/x_test.go"}); ok {
		t.Error("a cargo runner was narrowed as a go one")
	}
}
