package tdd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The exact shape issue #519 reported: a --old pattern that never matches the
// bytes on disk (stale CRLF, a typo, the wrong worktree) must never reach the
// suite at all — running it and reading "still green" as a survivor is the
// false positive the issue is about. Refusing BEFORE the run is what makes
// the fake runner below provably uncalled, not just cheap.
func TestRunMutantsProve_RefusesWhenTheOldPatternNeverMatches(t *testing.T) {
	root := makeGoRepo(t)
	src := "package m\n\nfunc Add(a, b int) int { return a + b }\n"
	write(t, root, "widget.go", src)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	called := false
	fakeRun := func(r Runner, root string) SuiteResult {
		called = true
		return SuiteResult{Passed: true}
	}

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a * b", // not present — the real source says "a + b"
		New:      "return a - b",
		WantFail: "TestAdd",
	}, fakeRun, &out, &errb)

	if code != ExitMutantsProveRefused {
		t.Fatalf("exit = %d, want ExitMutantsProveRefused (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveRefused, out.String(), errb.String())
	}
	if called {
		t.Fatal("the suite ran despite the mutation pattern matching nothing — exactly the false-survivor path issue #519 reports")
	}
	got, err := os.ReadFile(filepath.Join(root, "widget.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Fatalf("source file changed despite a refused mutation: %q", got)
	}
	if !strings.Contains(errb.String(), "0 matches") {
		t.Fatalf("stderr never says the pattern matched zero times: %q", errb.String())
	}
}

// The positive case: a mutation that DOES register in git diff --numstat and
// DOES fail the predicted test is reported as killed, and the file is
// restored byte-identically afterward.
func TestRunMutantsProve_ReportsKilledAndRestoresTheFileWhenTheNamedTestFails(t *testing.T) {
	root := makeGoRepo(t)
	src := "package m\n\nfunc Add(a, b int) int { return a + b }\n"
	write(t, root, "widget.go", src)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a + b",
		New:      "return a - b",
		WantFail: "TestAdd",
	}, RunSuite(precommitTestTimeout), &out, &errb)

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveKilled, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "TestAdd") {
		t.Fatalf("report never names the killed test: %q", out.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "widget.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Fatalf("file was not restored byte-identically after the proof: %q", got)
	}
}
