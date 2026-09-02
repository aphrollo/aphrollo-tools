package tdd

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// whatever was written to it. Precommit's per-file "unowned cargo package"
// note (and, from A2 on, its per-stage lines) is a genuine stderr side
// effect — the gate is a git hook, so stdout is reserved for git's own
// output — so this is the only way to pin that contract without inventing a
// parallel in-memory channel nothing else uses.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// makeMultiRootRepo builds a committed repo with THREE independent project
// roots sharing one git history: a cargo workspace at the repo root (member
// crates/a), and a pytest project nested at tools/py (its own pyproject.toml
// — a marker DetectRunner/FindProjectRoot must find before ever reaching the
// cargo Cargo.toml above it). This is the exact shape the bug report names:
// a Bevy-sized cargo workspace with a Python tool living inside it.
func makeMultiRootRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	write(t, root, "crates/a/Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/a/src/lib.rs", "pub fn base() -> i32 { 0 }\n")
	write(t, root, "tools/py/pyproject.toml", "[tool]\n")
	write(t, root, "tools/py/x.py", "def base():\n    return 1\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// TestPrecommit_MultiRoot_PytestSubdirNeverTouchesCargo pins the headline bug
// fix: staging a Python source+test pair under a pyproject.toml subdirectory
// of a cargo workspace must be judged by pytest IN THAT DIRECTORY — never by
// cargo at the repo root. Before this change, DetectRunner was always called
// on repoRoot, so a python-only commit under a Bevy-sized workspace paid a
// full `cargo nextest run` (20 minutes) for a change cargo has nothing to do
// with. The test file's content deliberately declares no `def test_` so the
// fail-first stage never engages — this test is about ROOT SELECTION, not
// fail-first.
func TestPrecommit_MultiRoot_PytestSubdirNeverTouchesCargo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeMultiRootRepo(t)
	write(t, root, "tools/py/test_x.py", "# no test function declared here\n")
	write(t, root, "tools/py/x.py", "def base():\n    return 2\n")
	gitDo(t, root, "add", ".")

	var seen []loggedRun
	res := Precommit(root, recordAllRuns(&seen, func(string) bool { return true }))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one run (pytest, scoped to tools/py), got %d: %+v", len(seen), seen)
	}
	got := seen[0]
	wantDir := root + string(os.PathSeparator) + "tools" + string(os.PathSeparator) + "py"
	if filepathClean(got.dir) != filepathClean(wantDir) {
		t.Fatalf("pytest must run IN tools/py, ran in %s", got.dir)
	}
	if got.runner.Cmd != "pytest" {
		t.Fatalf("expected pytest runner, got %+v", got.runner)
	}
	for _, r := range seen {
		if r.runner.Cmd == "cargo" {
			t.Fatalf("a python-only commit must never invoke cargo, ran: %+v", seen)
		}
	}
}

// TestPrecommit_MultiRoot_CargoMemberScopedToOwnPackage pins the companion
// case: a source edit inside a cargo WORKSPACE MEMBER (crates/a, which is its
// own project root because it carries its own Cargo.toml) runs cargo scoped
// to that package via -p — not the unscoped whole-workspace command. Per
// task A4, it executes from the resolved WORKSPACE root (Dir: root here),
// not the member's own directory, even though the run is grouped by the
// crate's own root (the `dir` param the fake SuiteRunner was called with).
func TestPrecommit_MultiRoot_CargoMemberScopedToOwnPackage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeMultiRootRepo(t)
	write(t, root, "crates/a/src/lib.rs", "pub fn base() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, recordRunner(&seen, root+string(os.PathSeparator)+"crates"+string(os.PathSeparator)+"a"))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}, Dir: root}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("cargo member mechanical run = %+v, want one %+v", seen, want)
	}
}

// TestPrecommit_UnownedCargoFile_SkippedWithNote pins the third required
// behavior: a staged .rs file that no [package] Cargo.toml owns (only the
// virtual workspace manifest above it) is SKIPPED — no cargo command runs at
// all for it — and a stderr note names the file. This is the fix for the
// "no owning package → unnarrowed full-workspace fallback" bug: previously
// ANY unowned staged cargo file widened the mechanical run to the entire
// workspace.
func TestPrecommit_UnownedCargoFile_SkippedWithNote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeMultiRootRepo(t)
	write(t, root, "misc.rs", "pub fn misc() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	var res GateResult
	stderr := captureStderr(t, func() {
		res = Precommit(root, recordRunner(&seen, root))
	})
	if res.Blocked {
		t.Fatalf("an unowned file must never block, got: %s", res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("an unowned cargo file must run NOTHING (no full-suite fallback), ran: %+v", seen)
	}
	wantNote := "gate precommit: misc.rs has no owning cargo package — not tested"
	if !strings.Contains(stderr, wantNote) {
		t.Fatalf("expected unowned-file note %q, got stderr: %q", wantNote, stderr)
	}
}

// TestPrecommit_MultiRoot_MixedCommit_BothRootsRun exercises a single commit
// that touches BOTH project roots at once (a cargo member AND the pytest
// subdirectory): each root must run its OWN scoped command, and neither
// root's runner leaks into the other's directory.
func TestPrecommit_MultiRoot_MixedCommit_BothRootsRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeMultiRootRepo(t)
	write(t, root, "crates/a/src/lib.rs", "pub fn base() -> i32 { 2 }\n")
	write(t, root, "tools/py/x.py", "def base():\n    return 3\n")
	gitDo(t, root, "add", ".")

	var seen []loggedRun
	res := Precommit(root, recordAllRuns(&seen, func(string) bool { return true }))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 2 {
		t.Fatalf("expected one run per touched root, got %d: %+v", len(seen), seen)
	}
	var sawCargo, sawPytest bool
	for _, r := range seen {
		switch r.runner.Cmd {
		case "cargo":
			sawCargo = true
			if !reflect.DeepEqual(r.runner, Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}, Dir: root}) {
				t.Fatalf("cargo run = %+v, want -p a from the workspace root %s", r.runner, root)
			}
		case "pytest":
			sawPytest = true
		}
	}
	if !sawCargo || !sawPytest {
		t.Fatalf("expected both a cargo and a pytest run, got %+v", seen)
	}
}

// filepathClean is a tiny local alias so this file doesn't need to import
// path/filepath just for Clean in the one assertion above.
func filepathClean(p string) string {
	return strings.TrimRight(strings.ReplaceAll(p, "/", string(os.PathSeparator)), string(os.PathSeparator))
}
