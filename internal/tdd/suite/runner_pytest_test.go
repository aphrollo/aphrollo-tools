package suite

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// pyRepo makes a repo top (a `.git` directory) holding the given files, each
// path slash-relative with the content given.
func pyRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestFindProjectRoot_PytestSignalsMakeASubdirectoryARoot: a Python directory
// that declares pytest is the root of the test files under it, however deep,
// and its runner is pytest, so the commit gate has something to run there.
func TestFindProjectRoot_PytestSignalsMakeASubdirectoryARoot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		want  string // root, repo-relative ("." is the repo top)
	}{
		{"requirements names pytest", map[string]string{"backend/requirements.txt": "fastapi\npytest==8.2\n"}, "backend"},
		{"a dev requirements file", map[string]string{"backend/requirements-dev.txt": "pytest-asyncio>=0.23\n"}, "backend"},
		{"requirements indented and cased", map[string]string{"backend/requirements.txt": "  PyTest\n"}, "backend"},
		{"setup.cfg names pytest", map[string]string{"backend/setup.cfg": "[tool:pytest]\ntestpaths = tests\n"}, "backend"},
		{"conftest above the tests dir", map[string]string{"backend/conftest.py": "", "backend/tests/conftest.py": ""}, "backend"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.files["backend/tests/test_a.py"] = "def test_a():\n    pass\n"
			repo := pyRepo(t, c.files)
			root := FindProjectRoot(filepath.Join(repo, "backend", "tests", "test_a.py"))
			if want := filepath.Join(repo, c.want); root != want {
				t.Fatalf("root = %q, want %q", root, want)
			}
			runner, ok := DetectRunner(root)
			if !ok || runner.Cmd != "pytest" {
				t.Fatalf("DetectRunner(%q) = %+v, %v; want pytest", root, runner, ok)
			}
		})
	}
}

// TestFindProjectRoot_APythonDirectoryNamingNoPytestStaysUnrun: a requirements
// file that only mentions pytest in a comment, or another package, declares
// nothing, so with no tests directory either the walk reaches the repo top
// and no runner is detected there.
func TestFindProjectRoot_APythonDirectoryNamingNoPytestStaysUnrun(t *testing.T) {
	t.Parallel()
	repo := pyRepo(t, map[string]string{
		"backend/requirements.txt": "# run pytest to test\nflask\nnot-pytest-plugin\n",
		"backend/app/service.py":   "def serve():\n    pass\n",
		"backend/setup.cfg":        "[flake8]\nmax-line-length = 100\n",
	})
	root := FindProjectRoot(filepath.Join(repo, "backend", "app", "service.py"))
	if root != repo {
		t.Fatalf("root = %q, want the repo top %q", root, repo)
	}
	if runner, ok := DetectRunner(root); ok {
		t.Fatalf("a repo top with no build file detected %+v", runner)
	}
}

// TestFindProjectRoot_ATestsDirWithRequirementsIsAPytestRoot is issue #992: a
// backend/ with a requirements file that does not name pytest (it is
// installed by hand, or by a CI step) and a tests/ directory of test_*.py is
// where `python -m pytest tests/<file>` runs from, so it is the root of the
// staged test and its runner is pytest.
func TestFindProjectRoot_ATestsDirWithRequirementsIsAPytestRoot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"test_ prefix", map[string]string{"backend/requirements.txt": "fastapi\n", "backend/tests/test_vault.py": ""}, "backend"},
		{"_test suffix", map[string]string{"backend/requirements-dev.txt": "fastapi\n", "backend/tests/vault_test.py": ""}, "backend"},
		{"the singular test dir", map[string]string{"backend/requirements.txt": "fastapi\n", "backend/test/test_vault.py": ""}, "backend"},
		{"no requirements file", map[string]string{"backend/tests/test_vault.py": ""}, "."},
		{"a tests dir holding no test file", map[string]string{"backend/requirements.txt": "fastapi\n", "backend/tests/helpers.py": "", "backend/tests/notes_test.txt": ""}, "."},
		{"a test file only below the tests dir", map[string]string{"backend/requirements.txt": "fastapi\n", "backend/tests/unit/test_vault.py": ""}, "."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.files["backend/app/vault.py"] = "def vault():\n    pass\n"
			repo := pyRepo(t, c.files)
			root := FindProjectRoot(filepath.Join(repo, "backend", "app", "vault.py"))
			if want := filepath.Join(repo, c.want); root != want {
				t.Fatalf("root = %q, want %q", root, want)
			}
			if _, ok := DetectRunner(root); ok != (c.want == "backend") {
				t.Fatalf("DetectRunner(%q) ok = %v, want %v", root, ok, c.want == "backend")
			}
		})
	}
}

// TestFindProjectRoot_APythonRootStopsAtTheNearestMarker: an ordinary marker
// nearer than a pytest signal keeps ending the walk, and a file that is not
// Python never takes the pytest signals into account.
func TestFindProjectRoot_APythonRootStopsAtTheNearestMarker(t *testing.T) {
	t.Parallel()
	repo := pyRepo(t, map[string]string{
		"backend/requirements.txt":     "pytest\n",
		"backend/tool/pyproject.toml":  "[project]\nname = 'tool'\n",
		"backend/tool/tests/test_b.py": "def test_b():\n    pass\n",
		"backend/notes/todo.md":        "x\n",
	})
	if got, want := FindProjectRoot(filepath.Join(repo, "backend", "tool", "tests", "test_b.py")), filepath.Join(repo, "backend", "tool"); got != want {
		t.Errorf("root = %q, want the nearer pyproject dir %q", got, want)
	}
	if got := FindProjectRoot(filepath.Join(repo, "backend", "notes", "todo.md")); got != repo {
		t.Errorf("a markdown file resolved to %q, want the repo top %q", got, repo)
	}
}

// TestFindProjectRoot_AConftestAtTheRepoTopIsTheRoot: with nothing else
// declaring pytest, the topmost conftest.py, the repo top's own included, is
// where the tests run from.
func TestFindProjectRoot_AConftestAtTheRepoTopIsTheRoot(t *testing.T) {
	t.Parallel()
	repo := pyRepo(t, map[string]string{"conftest.py": "", "tests/conftest.py": "", "tests/test_a.py": "def test_a():\n    pass\n"})
	if got := FindProjectRoot(filepath.Join(repo, "tests", "test_a.py")); got != repo {
		t.Fatalf("root = %q, want %q", got, repo)
	}
	if runner, ok := DetectRunner(repo); !ok || runner.Cmd != "pytest" {
		t.Fatalf("DetectRunner = %+v, %v; want pytest", runner, ok)
	}
}

// TestNarrowPytestFailFirst_RunsOnlyTheStagedTestFiles: the proof names the
// staged files pytest collects, in order, and never a conftest.py or a helper;
// no such file, or another runner, leaves the runner alone.
func TestNarrowPytestFailFirst_RunsOnlyTheStagedTestFiles(t *testing.T) {
	t.Parallel()
	r := Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "d"}
	got, ok := narrowPytestFailFirst(r, []string{"tests/conftest.py", "tests/test_a.py", "app/calc.py", "tests/b_test.py", "tests/test_data.json"})
	if want := []string{"-q", "tests/test_a.py", "tests/b_test.py"}; !ok || got.Cmd != "pytest" || !slices.Equal(got.Args, want) || got.Dir != "d" {
		t.Errorf("got %+v, %v; want pytest %v in d", got, ok, want)
	}
	if got, ok := narrowPytestFailFirst(r, []string{"tests/conftest.py", "app/calc.py"}); ok || !slices.Equal(got.Args, r.Args) {
		t.Errorf("no test file staged: got %+v, %v; want the runner unchanged", got, ok)
	}
	goRunner := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	if got, ok := narrowPytestFailFirst(goRunner, []string{"tests/test_a.py"}); ok || got.Cmd != "go" {
		t.Errorf("a go runner was narrowed: %+v, %v", got, ok)
	}
}
