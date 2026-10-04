package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenLaw is one deny law over every Go file with an escape comment and
// no baseline: any call to forbidden() is a commit refusal.
const forbiddenLaw = `
name = "no-forbidden"
description = "forbidden() is not called"
severity = "deny"
escape = "// forbidden-ok:"

[scope]
include = ["**/*.go", "README.md"]

[matcher]
kind = "regex-absent"
pattern = "forbidden\\("
`

// lawRepo is a committed Go repo carrying forbiddenLaw and one clean file.
func lawRepo(t *testing.T) string {
	t.Helper()
	root := mkProject(t)
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-forbidden.toml"), forbiddenLaw)
	mustWrite(t, filepath.Join(root, "widget.go"), "package m\n\nfunc Size() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "init")
	return root
}

// An edit that leaves a law hit the commit gate refuses says so on its gate
// line, with the escape, so the lane meets the refusal at the edit and not
// at `git commit` (issue #968).
func TestPostEdit_PutsAWouldBeRatchetRefusalOnTheGateLine(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, "package m\n\nfunc Size() int { return forbidden() }\n")

	got := PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	line := firstLine(got)
	for _, want := range []string{"→ green", "ratchet would refuse the commit", "no-forbidden: widget.go:3", "// forbidden-ok:"} {
		if !strings.Contains(line, want) {
			t.Fatalf("gate line %q does not carry %q (full: %q)", line, want, got)
		}
	}
}

// A clean edit's gate line says nothing about the laws.
func TestPostEdit_SaysNothingOfTheLawsForACleanEdit(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, "package m\n\nfunc Size() int { return 2 }\n")

	got := PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	if strings.Contains(got, "ratchet") {
		t.Fatalf("a clean edit must say nothing of the laws, got %q", got)
	}
}

// A file no suite runs for still gets its laws judged, and the refusal is
// then the whole gate line.
func TestPostEdit_NamesARefusalInAFileNoSuiteRunsFor(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	readme := filepath.Join(root, "README.md")
	mustWrite(t, readme, "call forbidden() here\n")

	got := PostEdit(postPayload("Write", readme), fakeRun(false, "must not run"))

	if !strings.HasPrefix(got, "gate: ratchet would refuse the commit: no-forbidden: README.md:1") {
		t.Fatalf("got %q, want the refusal as the gate line", got)
	}
}

// A shell write fires no pre-edit hook, so the post-edit harvest is the
// only place its law hits can be named before the commit.
func TestPostBash_NamesAWouldBeRatchetRefusalOfAShellWrite(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	cmd := "cat > widget.go"
	PreBash(bashPayload(t, "s968", root, cmd))
	if err := os.WriteFile(filepath.Join(root, "widget.go"), []byte("package m\n\nfunc Size() int { return forbidden() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var dirs []string
	got := PostBash(bashPayload(t, "s968", root, cmd), recordSuiteDirs(&dirs))

	if !strings.Contains(got, "ratchet would refuse the commit: no-forbidden: widget.go:3") {
		t.Fatalf("got %q, want the shell write's refusal named", got)
	}
}
