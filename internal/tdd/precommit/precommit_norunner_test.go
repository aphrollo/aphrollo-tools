package precommit

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateRoot_NoRunnerNamesEachUntestedRootAndItsFiles is issue #992: a
// commit staging tests under backend/ and web/ of a repo whose top carries no
// runner marker printed one line naming the whole tree, "skipped (no detected
// runner)". It now says NOT RUN, why, and each top-level directory with the
// staged files nothing was tested in.
func TestGateRoot_NoRunnerNamesEachUntestedRootAndItsFiles(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "backend/app/vault.py", "x = 1\n")
	write(t, root, "backend/tests/test_vault.py", "def test_v():\n    pass\n")
	write(t, root, "web/util.py", "y = 2\n")
	gitDo(t, root, "add", ".")
	groups := stagedRootGroups(root)
	if len(groups) != 1 || groups[0].Root != root {
		t.Fatalf("groups = %+v, want the one repo-top group", groups)
	}

	var res GateResult
	stderr := captureStderr(t, func() {
		res = gateRoot("precommit", root, groups[0], RunSuite(precommitTestTimeout), true)
	})
	if res.Blocked {
		t.Fatalf("a root with no runner refused the commit: %s", res.Message)
	}
	for _, want := range []string{
		"gate precommit: " + root + " → NOT RUN — no test runner is detected for 3 staged file(s)",
		"nothing was tested",
		"backend/: backend/app/vault.py, backend/tests/test_vault.py",
		"web/: web/util.py",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "skipped (no detected runner)") {
		t.Errorf("the whole-tree skip line is back:\n%s", stderr)
	}
}

// TestUntestedRootLines_ListsAtMostFiveFilesPerRoot pins the cap: five files
// are named, a sixth is counted.
func TestUntestedRootLines_ListsAtMostFiveFilesPerRoot(t *testing.T) {
	var five, six []string
	for i := range 6 {
		f := fmt.Sprintf("svc/f%d.py", i)
		six = append(six, f)
		if i < 5 {
			five = append(five, f)
		}
	}
	got := untestedRootLines(five)
	if want := "  svc/: svc/f0.py, svc/f1.py, svc/f2.py, svc/f3.py, svc/f4.py"; len(got) != 1 || got[0] != want {
		t.Errorf("five files: %q, want [%q]", got, want)
	}
	got = untestedRootLines(six)
	if want := "  svc/: svc/f0.py, svc/f1.py, svc/f2.py, svc/f3.py, svc/f4.py and 1 more"; len(got) != 1 || got[0] != want {
		t.Errorf("six files: %q, want [%q]", got, want)
	}
}

// TestUntestedRootLines_AFileAtTheRepoTopIsItsOwnRoot: a file with no
// directory belongs to the top, named "./", and roots come out sorted.
func TestUntestedRootLines_AFileAtTheRepoTopIsItsOwnRoot(t *testing.T) {
	got := untestedRootLines([]string{"z/a.py", "main.py", filepath.ToSlash("a/b/c.py")})
	want := []string{"  ./: main.py", "  a/: a/b/c.py", "  z/: z/a.py"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}
