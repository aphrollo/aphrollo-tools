package precommit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// A repo whose Python lives in backend/ and declares pytest only in a
// requirements file used to say "skipped (no detected runner)" for a staged
// test under backend/tests, and never proved it red. backend/ is a pytest
// root, and the proof runs `python -m pytest` there.

// fakePython answers the pytest import probe, and as pytest passes when
// app/calc.py adds and fails naming the staged test otherwise.
const fakePython = `#!/bin/sh
if [ "$1" = "-c" ]; then exit 0; fi
if grep -q 'a + b' app/calc.py; then echo "1 passed in 0.01s"; exit 0; fi
echo "FAILED tests/test_calc.py::test_add - assert 0 == 3"
echo "1 failed in 0.01s"
exit 1
`

// makeBackendPytestRepo commits a repo with backend/requirements.txt naming
// pytest and a calc.py returning 0, gives backend a fake virtualenv python,
// and stages a calc.py that adds plus a new test for it.
func makeBackendPytestRepo(t *testing.T, python string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake interpreter is a sh script") // skip-ok: the fake pytest is a POSIX shell script
	}
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", ".venv/\n")
	write(t, root, "backend/requirements.txt", "fastapi\npytest==8.2\n")
	write(t, root, "backend/app/calc.py", "def add(a, b):\n    return 0\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	bin := filepath.Join(root, "backend", ".venv", "bin", "python")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := proc.WriteExecutable(bin, []byte(python), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "backend/app/calc.py", "def add(a, b):\n    return a + b\n")
	write(t, root, "backend/tests/test_calc.py", "from app.calc import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n")
	gitDo(t, root, "add", "backend/app/calc.py", "backend/tests/test_calc.py")
	return root
}

// TestGateRoot_ABackendPytestRootIsProvenRedAtHead: the staged test under
// backend/tests is found under the backend root, is red against HEAD's calc.py
// and green with the change, and the gate names no missing runner.
func TestGateRoot_ABackendPytestRootIsProvenRedAtHead(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeBackendPytestRepo(t, fakePython)

	groups := stagedRootGroups(root)
	if len(groups) != 1 || groups[0].Root != filepath.Join(root, "backend") {
		t.Fatalf("groups = %+v, want the one backend root", groups)
	}
	var res GateResult
	stderr := captureStderr(t, func() {
		res = gateRoot("precommit", root, groups[0], RunSuite(precommitTestTimeout), true)
	})
	if res.Blocked {
		t.Fatalf("red at HEAD and green with the change was refused:\n%s\n%s", res.Message, stderr)
	}
	if strings.Contains(stderr, "no detected runner") {
		t.Fatalf("the backend root still reports no runner:\n%s", stderr)
	}
	if !strings.Contains(stderr, "→ red-proven") || !strings.Contains(stderr, "→ green-proven") {
		t.Fatalf("want red-proven then green-proven, got:\n%s", stderr)
	}
	if line := failFirstLine(stderr); !strings.Contains(line, filepath.Join(".venv", "bin", "python")+" -m pytest") {
		t.Errorf("the proof did not run pytest under the root's interpreter: %q", line)
	}
}

// TestGateRoot_ABackendPytestRootWithoutPytestSaysNotRun: when every
// interpreter the proof can find, the root's virtualenv and both on PATH,
// lacks pytest, the proof ends with a NOT RUN line naming the root and the
// reason, and the commit is not refused.
func TestGateRoot_ABackendPytestRootWithoutPytestSaysNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeBackendPytestRepo(t, "#!/bin/sh\nexit 1\n")
	groups := stagedRootGroups(root)
	bin := t.TempDir()
	for _, name := range []string{"python3", "python"} {
		if err := proc.WriteExecutable(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var res GateResult
	stderr := captureStderr(t, func() {
		res = gateRoot("precommit", root, groups[0], RunSuite(precommitTestTimeout), true)
	})
	if res.Blocked {
		t.Fatalf("a proof that could not run refused the commit:\n%s", res.Message)
	}
	want := "fail-first in " + filepath.Join(root, "backend") + " → NOT RUN — pytest is not importable by "
	if !strings.Contains(stderr, want) {
		t.Fatalf("no NOT RUN line naming the root and the reason; want %q in:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "red-proven") {
		t.Fatalf("a proof that never ran claimed a red:\n%s", stderr)
	}
}
