package lawgate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const graphEditLaw = `
name = "no_reach_b"
description = "package a never reaches package b"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "go-dep-graph-forbids"
roots = ["example.com/m/a"]
forbidden = ["example.com/m/b"]
`

const graphEditA = "package a\n\nimport _ \"fmt\"\n\nfunc Answer() int { return 1 }\n"

// graphEditRepo is a real one-module git tree with a graph law forbidding
// package a from reaching package b, and a fresh state dir for the cache.
func graphEditRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	tddtest.GitInit(t, root)
	tddtest.MustWrite(t, filepath.Join(root, ".ratchet", "laws", "no_reach_b.toml"), graphEditLaw)
	tddtest.MustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.21\n")
	tddtest.MustWrite(t, filepath.Join(root, "a", "a.go"), graphEditA)
	tddtest.MustWrite(t, filepath.Join(root, "b", "b.go"), "package b\n")
	tddtest.MustWrite(t, filepath.Join(root, "README.md"), "# m\n")
	return root
}

// countGoListRuns puts a `go` on PATH that logs each `go list` before
// running the real one, and returns a reader of that count.
func countGoListRuns(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$1\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		data, _ := os.ReadFile(log)
		n := 0
		for _, line := range strings.Split(string(data), "\n") {
			if line == "list" {
				n++
			}
		}
		return n
	}
}

func TestRatchetAdvisory_RefusesAnEditThatAddsAnImportBreakingAGraphLaw(t *testing.T) {
	root := graphEditRepo(t)
	path := filepath.Join(root, "a", "a.go")
	raw := ratchetPayload(t, "Edit", path, map[string]any{
		"old_string": "import _ \"fmt\"",
		"new_string": "import _ \"fmt\"\nimport _ \"example.com/m/b\"",
	})
	d := RatchetAdvisory(raw)
	if d.Action != Block {
		t.Fatalf("action = %v, want Block (reason: %q)", d.Action, d.Reason)
	}
	if want := "example.com/m/a->example.com/m/b"; !strings.Contains(d.Reason, want) {
		t.Errorf("reason %q does not name the reach %q", d.Reason, want)
	}
	if got, _ := os.ReadFile(path); string(got) != graphEditA {
		t.Errorf("the judged edit changed a/a.go on disk: %q", got)
	}
}

func TestRatchetAdvisory_RefusesAWriteThatAddsAFileBreakingAGraphLaw(t *testing.T) {
	root := graphEditRepo(t)
	raw := ratchetPayload(t, "Write", filepath.Join(root, "a", "extra.go"), map[string]any{
		"content": "package a\n\nimport _ \"example.com/m/b\"\n",
	})
	if d := RatchetAdvisory(raw); d.Action != Block {
		t.Fatalf("action = %v, want Block (reason: %q)", d.Action, d.Reason)
	}
}

func TestRatchetAdvisory_RunsNoGoListForAnEditThatCannotChangeTheGraph(t *testing.T) {
	root := graphEditRepo(t)
	calls := countGoListRuns(t)
	edits := map[string]map[string]any{
		filepath.Join(root, "a", "a.go"): {"old_string": "return 1", "new_string": "return 2"},
		filepath.Join(root, "README.md"): {"old_string": "# m", "new_string": "# module m"},
	}
	for path, fields := range edits {
		if d := RatchetAdvisory(ratchetPayload(t, "Edit", path, fields)); d.Action != Allow {
			t.Fatalf("%s: action = %v (%s), want Allow", path, d.Action, d.Reason)
		}
	}
	if n := calls(); n != 0 {
		t.Fatalf("go list ran %d time(s) for edits that touch no import, want 0", n)
	}
}

func TestRatchetAdvisory_RunsGoListOnceForAnImportEditAndCachesItForTheNext(t *testing.T) {
	root := graphEditRepo(t)
	calls := countGoListRuns(t)
	raw := ratchetPayload(t, "Edit", filepath.Join(root, "a", "a.go"), map[string]any{
		"old_string": "import _ \"fmt\"",
		"new_string": "import _ \"fmt\"\nimport _ \"strings\"",
	})
	for i := 0; i < 2; i++ {
		if d := RatchetAdvisory(raw); d.Action != Allow {
			t.Fatalf("run %d: action = %v (%s), want Allow", i, d.Action, d.Reason)
		}
	}
	if n := calls(); n != 1 {
		t.Fatalf("go list ran %d times for the same import edit twice, want 1", n)
	}
}

func TestGraphMayChange_ByWhatTheEditTouches(t *testing.T) {
	const pkg = "package a\n\nimport \"fmt\"\n\nfunc F() { fmt.Println() }\n"
	cases := []struct {
		name          string
		rel           string
		existed       bool
		before, after string
		want          bool
	}{
		{"body edit", "a/a.go", true, pkg, strings.Replace(pkg, "Println()", "Println(1)", 1), false},
		{"import added", "a/a.go", true, pkg, strings.Replace(pkg, "import \"fmt\"", "import (\n\t\"fmt\"\n\t\"os\"\n)", 1), true},
		{"import removed", "a/a.go", true, strings.Replace(pkg, "import \"fmt\"", "import (\n\t\"fmt\"\n\t\"os\"\n)", 1), pkg, true},
		{"build constraint added", "a/a.go", true, pkg, "//go:build linux\n\n" + pkg, true},
		{"package clause renamed", "a/a.go", true, pkg, strings.Replace(pkg, "package a", "package z", 1), true},
		{"new go file", "a/n.go", false, "", pkg, true},
		{"file with no imports edited", "a/a.go", true, "package a\n\nfunc F() {}\n", "package a\n\nfunc F() int { return 1 }\n", false},
		{"first import added to a file with none", "a/a.go", true, "package a\n\nfunc F() {}\n", "package a\n\nimport \"os\"\n\nfunc F() {}\n", true},
		{"unparseable after", "a/a.go", true, pkg, "package a\n\nimport (\n", true},
		{"unparseable before", "a/a.go", true, "import (\n", pkg, true},
		{"markdown", "README.md", true, "a", "b", false},
		{"go.mod", "go.mod", true, "module m\n", "module m\n\nrequire x v1\n", true},
		{"go.work", "go.work", true, "go 1.21\n", "go 1.22\n", true},
		{"go.sum", "go.sum", true, "", "x\n", true},
		{"body edit in a nested go.mod's package", "sub/deep/x.go", true, pkg, pkg + "// c\n", false},
	}
	for _, c := range cases {
		if got := graphMayChange(c.rel, c.existed, c.before, c.after); got != c.want {
			t.Errorf("%s: graphMayChange = %v, want %v", c.name, got, c.want)
		}
	}
}
