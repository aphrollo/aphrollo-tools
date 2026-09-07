package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A proof that never names what it expects to fail is not a proof — the
// four flags are all required, and an omission is a usage error, not a
// defaulted-away no-op.
func TestGateMutantsProve_RequiresAllFourFlags(t *testing.T) {
	gateConfigDir(t)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "prove", "--file", "x.go", "--old", "a", "--new", "b"},
		strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (missing --want-fail)\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--want-fail") {
		t.Fatalf("stderr never names the missing flag: %q", errb.String())
	}
}

// The wiring end to end: a real mutation that verifiably registers in
// `git diff --numstat` and fails the named test reports exit 0, and the file
// comes back byte-identical.
func TestGateMutantsProve_EndToEndKillsTheNamedTest(t *testing.T) {
	gateConfigDir(t)
	isolateGit(t)

	root := t.TempDir()
	gitInitRepo(t, root)
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/prove\n\ngo 1.26\n")
	src := "package prove\n\nfunc Add(a, b int) int { return a + b }\n"
	writeFile(t, filepath.Join(root, "widget.go"), src)
	writeFile(t, filepath.Join(root, "widget_test.go"),
		"package prove\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitCommitAll(t, root, "base")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "prove",
		"--file", filepath.Join(root, "widget.go"),
		"--old", "return a + b",
		"--new", "return a - b",
		"--want-fail", "TestAdd",
	}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "TestAdd") {
		t.Fatalf("stdout never names the killed test: %q", out.String())
	}
	if got := readFile(t, filepath.Join(root, "widget.go")); got != src {
		t.Fatalf("file not restored: %q", got)
	}
}
