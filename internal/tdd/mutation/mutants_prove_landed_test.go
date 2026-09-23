package mutation

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The "mutation landed" check was `git diff --numstat` on the file: non-empty
// meant the mutation registered. That reads the file against the INDEX, not
// against the bytes the proof started from, so a file already carrying an
// unstaged edit passed on that edit alone, whatever the mutation did.
//
// The mutation here changes only a line ending, in a file whose attributes
// normalise line endings to LF: the content git (and the compiler) reads is
// identical before and after, so nothing was mutated. On a clean file the old
// check refused it; with an unrelated unstaged edit above it, it ran the
// suite and judged a mutation that never happened.
func TestRunMutantsProve_AFileWithUnstagedEditsStillNeedsTheMutationItselfToLand(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, ".gitattributes", "*.go text eol=lf\n")
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	// Work in progress the proof must not mistake for its own mutation.
	dirty := "package m\n\n// Add sums its arguments.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	write(t, root, "widget.go", dirty)

	ran := false
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a + b\n",
		New:      "return a + b\r\n",
		WantFail: "TestAdd",
	}, func(Runner, string) SuiteResult {
		ran = true
		return SuiteResult{Passed: true, Output: "ok  \texample.com/m\t0.003s\n"}
	}, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveRefused {
		t.Fatalf("exit = %d, want ExitMutantsProveRefused (%d): a mutation that changed nothing git reads "+
			"passed on the file's unstaged edit:\n%s", code, ExitMutantsProveRefused, report)
	}
	if ran {
		t.Errorf("a suite ran over a mutation that never landed:\n%s", report)
	}
	if got, err := os.ReadFile(filepath.Join(root, "widget.go")); err != nil {
		t.Fatal(err)
	} else if string(got) != dirty {
		t.Fatalf("the unstaged work was not restored byte-identically: %q", got)
	}
}

// The other side: a real mutation in a file with unstaged edits is proved as
// usual, judged against the edited bytes the proof started from. A check that refused every dirty file would be safe and useless.
func TestRunMutantsProve_ARealMutationInAFileWithUnstagedEditsIsProved(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "widget.go", "package m\n\n// Add sums its arguments.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n")

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a + b",
		New:      "return a - b",
		WantFail: "TestAdd",
	}, func(Runner, string) SuiteResult {
		return SuiteResult{Output: "--- FAIL: TestAdd (0.00s)\nFAIL\nFAIL\texample.com/m\t0.003s\n"}
	}, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s", code, ExitMutantsProveKilled, report)
	}
	if !strings.Contains(report, "TestAdd") {
		t.Errorf("the verdict never names the killed test:\n%s", report)
	}
}

// The same no-op from the other side: the working copy is CRLF throughout (an
// editor rewrote it; the attributes normalise to LF), and the mutation turns
// one CRLF back into LF. The starting bytes must be read through the same
// attributes as the mutated file, or the raw CRs alone look like a change.
func TestRunMutantsProve_ALineEndingOnlyMutationOfACRLFWorkingCopyIsRefused(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, ".gitattributes", "*.go text eol=lf\n")
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	crlf := "package m\r\n\r\nfunc Add(a, b int) int {\r\n\treturn a + b\r\n}\r\n"
	write(t, root, "widget.go", crlf)

	ran := false
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a + b\r\n",
		New:      "return a + b\n",
		WantFail: "TestAdd",
	}, func(Runner, string) SuiteResult {
		ran = true
		return SuiteResult{Passed: true, Output: "ok  \texample.com/m\t0.003s\n"}
	}, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveRefused {
		t.Fatalf("exit = %d, want ExitMutantsProveRefused (%d): a line-ending-only edit was judged as a "+
			"mutation:\n%s", code, ExitMutantsProveRefused, report)
	}
	if ran {
		t.Errorf("a suite ran over a mutation that never landed:\n%s", report)
	}
	if got, err := os.ReadFile(filepath.Join(root, "widget.go")); err != nil {
		t.Fatal(err)
	} else if string(got) != crlf {
		t.Fatalf("the CRLF working copy was not restored byte-identically: %q", got)
	}
}
