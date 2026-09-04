package tdd

import (
	"bytes"
	"strings"
	"testing"
)

// A pure move of a function between two Go files must not start gremlins at
// all — every changed line in both files is the same line relocated — and
// the receipt has to say why its count is zero rather than leaving that
// silent (issue: the Go runner never populated moved_lines, so a lane like
// this one re-mutated code nobody had touched).
func TestRunGoMutantsCI_APureMoveBetweenFilesMeasuresZeroAndRecordsMovedLines(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "calc.go", strings.Join([]string{
		"package m",
		"",
		"func Keep() int { return 1 }",
		"",
		"func Moving() int {",
		"	if true {",
		"		return 2",
		"	}",
		"	return 3",
		"}",
	}, "\n")+"\n")
	write(t, root, "other.go", "package m\n\nfunc Other() int { return 7 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "base")
	base := gitValue(t, root, "rev-parse", "HEAD")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")

	write(t, root, "calc.go", "package m\n\nfunc Keep() int { return 1 }\n")
	write(t, root, "other.go", strings.Join([]string{
		"package m",
		"",
		"func Other() int { return 7 }",
		"",
		"func Moving() int {",
		"	if true {",
		"		return 2",
		"	}",
		"	return 3",
		"}",
	}, "\n")+"\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "move Moving to other.go")

	ran := fakeGremlins(t, "", 0)
	var out bytes.Buffer
	code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base}, &out)

	if len(*ran) != 0 {
		t.Fatalf("gremlins ran for a lane that only moved code: %+v", *ran)
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0 — a pure move is a real zero-mutant answer:\n%s", code, out.String())
	}
	r := readReceipt(t, gitValue(t, root, "rev-parse", "HEAD:"))
	if r.MovedLines == 0 {
		t.Fatal("the receipt does not record how many lines moved, so nothing says why it measured nothing")
	}
}
