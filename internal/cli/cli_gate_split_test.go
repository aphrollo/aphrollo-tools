package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const splitPassedJSON = `{"Action":"run","Package":"example.com/m","Test":"TestWidget"}
{"Action":"pass","Package":"example.com/m","Test":"TestWidget","Elapsed":0}
{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

// stubSplitSuite answers the proof's suite runs with res for the test.
func stubSplitSuite(t *testing.T, res tdd.SuiteResult) {
	t.Helper()
	prev := splitCommitSuite
	t.Cleanup(func() { splitCommitSuite = prev })
	splitCommitSuite = func(tdd.Runner, string) tdd.SuiteResult { return res }
}

func splitGreen() tdd.SuiteResult {
	return tdd.SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: splitPassedJSON}
}

// stagedMixedRepo is a Go repo whose staged change adds a test that passes at
// HEAD and the source beside it; the working directory is the repo.
func stagedMixedRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	gateConfigDir(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	writeFile(t, root+"/go.mod", "module example.com/m\n\ngo 1.26\n")
	gitCommitAll(t, root, "base")
	writeFile(t, root+"/widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	writeFile(t, root+"/widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitRun(t, root, "add", "-A")
	t.Chdir(root)
	return root
}

func runSplit(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Run(append([]string{"gate", "split-commit"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestGateSplitCommit_DryRunNamesBothCommitsAndChangesNothing(t *testing.T) {
	root := stagedMixedRepo(t)
	stubSplitSuite(t, splitGreen())
	head := gitLine(t, root, "rev-parse", "HEAD")
	index := gitLine(t, root, "write-tree")

	code, out, errb := runSplit()

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	for _, want := range []string{"dry run", "widget_test.go", "TestWidget", "widget.go", "--apply"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := gitLine(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s on a dry run", got)
	}
	if got := gitLine(t, root, "write-tree"); got != index {
		t.Errorf("index changed on a dry run: %s -> %s", index, got)
	}
}

func TestGateSplitCommit_ApplyCommitsTheTestsAloneWithTheGivenMessage(t *testing.T) {
	root := stagedMixedRepo(t)
	stubSplitSuite(t, splitGreen())

	code, out, errb := runSplit("--apply", "-m", "Pin Widget")

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if files := gitLine(t, root, "show", "--name-only", "--format=", "HEAD"); files != "widget_test.go" {
		t.Errorf("commit files = %q, want only widget_test.go", files)
	}
	if msg := gitLine(t, root, "log", "-1", "--format=%B"); msg != "Pin Widget" {
		t.Errorf("message = %q", msg)
	}
	if staged := gitLine(t, root, "diff", "--cached", "--name-only"); staged != "widget.go" {
		t.Errorf("still staged = %q, want widget.go", staged)
	}
	if !strings.Contains(out, "git commit") {
		t.Errorf("output should say the rest is committed with git commit:\n%s", out)
	}
}

func TestGateSplitCommit_ATestRedAtHeadHasNothingToSplit(t *testing.T) {
	root := stagedMixedRepo(t)
	stubSplitSuite(t, tdd.SuiteResult{Passed: false, Output: "--- FAIL: TestWidget (0.00s)\n    widget_test.go:7: no\nFAIL\n"})
	head := gitLine(t, root, "rev-parse", "HEAD")

	code, _, errb := runSplit("--apply")

	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errb)
	}
	if !strings.Contains(errb, "nothing to split") {
		t.Errorf("stderr should say there is nothing to split:\n%s", errb)
	}
	if got := gitLine(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
}

func TestGateSplitCommit_RefusesABadFlag(t *testing.T) {
	if code, _, _ := runSplit("--nope"); code != 2 {
		t.Fatalf("exit %d, want 2 for an unknown flag", code)
	}
}

func TestPrintSplitPlan_ShowsTheGivenMessageElseThePlansDefault(t *testing.T) {
	plan := tdd.SplitPlan{
		Tests:   []string{"a_test.go"},
		Names:   []string{"TestA"},
		Rest:    []string{"a.go"},
		Message: "Default subject\n\nTests: TestA",
	}
	cases := []struct {
		name, msg, want string
	}{
		{"given", "Given subject", "  message: Given subject\n"},
		{"empty", "", "  message: Default subject\n"},
		{"blank", " \t\n", "  message: Default subject\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			printSplitPlan(&out, plan, tc.msg)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("output lacks %q:\n%s", tc.want, out.String())
			}
		})
	}
}

func TestPrintSplitPlan_OmitsTheTestsLineWhenNoNameIsKnown(t *testing.T) {
	var out bytes.Buffer

	printSplitPlan(&out, tdd.SplitPlan{Tests: []string{"a_test.go"}, Rest: []string{"a.go"}, Message: "Subject"}, "")

	if strings.Contains(out.String(), "tests:") {
		t.Fatalf("no names known, yet the output lists tests:\n%s", out.String())
	}
	want := "commit 1 (tests only, already green at HEAD):\n    a_test.go\n  message: Subject\ncommit 2 (the rest, still staged afterwards):\n    a.go\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

// The plumbing commit must not become a way onto main in the primary
// checkout: the shim's own wall answers for it.
func TestGateSplitCommit_ApplyIsRefusedInThePrimaryCheckout(t *testing.T) {
	primary, _, _ := primaryShimRepo(t)
	gateConfigDir(t)
	stubSplitSuite(t, splitGreen())
	writeFile(t, primary+"/go.mod", "module example.com/m\n\ngo 1.26\n")
	gitCommitAll(t, primary, "go mod")
	writeFile(t, primary+"/widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	writeFile(t, primary+"/widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitRun(t, primary, "add", "-A")
	head := gitLine(t, primary, "rev-parse", "HEAD")

	code, _, errb := runSplit("--apply")

	if code != 1 || !strings.Contains(errb, "refused") || !strings.Contains(errb, "merge-only") {
		t.Fatalf("exit %d, stderr %q; want the primary-checkout refusal", code, errb)
	}
	if got := gitLine(t, primary, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s in the primary checkout", got)
	}
}

// A -m message is judged like the commit-msg hook judges one: the
// attribution rule and the subject rule both refuse.
func TestGateSplitCommit_ApplyRefusesAMessageTheCommitMsgGateRefuses(t *testing.T) {
	cases := []struct{ name, msg, want string }{
		{"attribution trailer", "Pin the widget behavior with a test\n\nCo-Authored-By: Claude <noreply@anthropic.com>", "Co-Authored-By"},
		{"short subject", "Pin Widget", "under four words"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := stagedMixedRepo(t)
			stubSplitSuite(t, splitGreen())
			head := gitLine(t, root, "rev-parse", "HEAD")
			writeFile(t, root+"/aphrollo.toml", "[aphrollo]\nundercover = true\n")
			gitRun(t, root, "add", "aphrollo.toml")

			code, _, errb := runSplit("--apply", "-m", tc.msg)

			if code != 1 || !strings.Contains(errb, "refused") || !strings.Contains(errb, tc.want) {
				t.Fatalf("exit %d, stderr %q; want a commit-msg refusal naming %q", code, errb, tc.want)
			}
			if got := gitLine(t, root, "rev-parse", "HEAD"); got != head {
				t.Errorf("HEAD moved to %s", got)
			}
		})
	}
}
