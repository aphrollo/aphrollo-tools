package suite

import (
	"os"
	"path/filepath"
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
// nothing, so the walk reaches the repo top and no runner is detected there.
func TestFindProjectRoot_APythonDirectoryNamingNoPytestStaysUnrun(t *testing.T) {
	t.Parallel()
	repo := pyRepo(t, map[string]string{
		"backend/requirements.txt": "# run pytest to test\nflask\nnot-pytest-plugin\n",
		"backend/tests/test_a.py":  "def test_a():\n    pass\n",
		"backend/setup.cfg":        "[flake8]\nmax-line-length = 100\n",
	})
	root := FindProjectRoot(filepath.Join(repo, "backend", "tests", "test_a.py"))
	if root != repo {
		t.Fatalf("root = %q, want the repo top %q", root, repo)
	}
	if runner, ok := DetectRunner(root); ok {
		t.Fatalf("a repo top with no build file detected %+v", runner)
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
