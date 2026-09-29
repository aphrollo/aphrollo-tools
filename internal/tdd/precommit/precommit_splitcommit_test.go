package precommit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const splitPassedJSON = `{"Action":"run","Package":"example.com/m","Test":"TestWidget"}
{"Action":"pass","Package":"example.com/m","Test":"TestWidget","Elapsed":0}
{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

// splitGreenRun is a suite whose tests all pass, at HEAD or anywhere else.
func splitGreenRun(Runner, string) SuiteResult {
	return SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: splitPassedJSON}
}

// splitRedRun is a suite whose tests fail, as a new test does against HEAD.
func splitRedRun(Runner, string) SuiteResult {
	return SuiteResult{Passed: false, Output: "--- FAIL: TestWidget (0.00s)\n    widget_test.go:7: no\nFAIL\n"}
}

// splitMixedRepo stages one test that passes at HEAD and the source beside it.
func splitMixedRepo(t *testing.T) string {
	t.Helper()
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	return root
}

// Serial: captures the process-wide os.Stderr.
func TestPlanSplit_NamesTheGreenTestsAndWhatStaysStaged(t *testing.T) {
	root := splitMixedRepo(t)

	var plan SplitPlan
	var err error
	captureStderr(t, func() { plan, err = PlanSplit(root, splitGreenRun) })

	if err != nil {
		t.Fatalf("PlanSplit: %v", err)
	}
	if !reflect.DeepEqual(plan.Tests, []string{"widget_test.go"}) {
		t.Errorf("Tests = %v, want [widget_test.go]", plan.Tests)
	}
	if !reflect.DeepEqual(plan.Names, []string{"TestWidget"}) {
		t.Errorf("Names = %v, want [TestWidget]", plan.Names)
	}
	if !reflect.DeepEqual(plan.Rest, []string{"widget.go"}) {
		t.Errorf("Rest = %v, want [widget.go]", plan.Rest)
	}
	if !strings.Contains(plan.Message, "TestWidget") {
		t.Errorf("default message %q should name the tests", plan.Message)
	}
}

// A dry run changes nothing: not HEAD, not the index.
// Serial: captures the process-wide os.Stderr.
func TestPlanSplit_ChangesNothing(t *testing.T) {
	root := splitMixedRepo(t)
	head := gitOutT(t, root, "rev-parse", "HEAD")
	index := gitOutT(t, root, "write-tree")

	captureStderr(t, func() { _, _ = PlanSplit(root, splitGreenRun) })

	if got := gitOutT(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
	if got := gitOutT(t, root, "write-tree"); got != index {
		t.Errorf("index changed: %s -> %s", index, got)
	}
}

// Tests that go RED at HEAD are what the gate wants: there is nothing to split.
// Serial: captures the process-wide os.Stderr.
func TestPlanSplit_ATestRedAtHeadHasNothingToSplit(t *testing.T) {
	root := splitMixedRepo(t)

	var plan SplitPlan
	var err error
	captureStderr(t, func() { plan, err = PlanSplit(root, splitRedRun) })

	if err != nil {
		t.Fatalf("PlanSplit: %v", err)
	}
	if len(plan.Tests) != 0 {
		t.Errorf("Tests = %v, want none for a red test", plan.Tests)
	}
}

// Nothing staged is nothing to split, and it is not an error.
func TestPlanSplit_NothingStagedHasNothingToSplit(t *testing.T) {
	root := makeGoRepo(t)

	plan, err := PlanSplit(root, splitGreenRun)

	if err != nil || len(plan.Tests) != 0 {
		t.Fatalf("PlanSplit = %+v, %v; want an empty plan", plan, err)
	}
}

// Applying the plan commits the tests alone, keeps the rest staged, and
// leaves every file of the working tree as it was.
// Serial: captures the process-wide os.Stderr.
func TestApplySplit_CommitsTheTestsAloneAndKeepsTheRestStaged(t *testing.T) {
	root := splitMixedRepo(t)
	edited := "package m\n\nfunc Widget() int { return 1 } // edited after staging\n"
	write(t, root, "widget.go", edited)
	var plan SplitPlan
	captureStderr(t, func() { plan, _ = PlanSplit(root, splitGreenRun) })

	commit, err := ApplySplit(root, plan, "Pin Widget with a test")
	if err != nil {
		t.Fatalf("ApplySplit: %v", err)
	}

	if head := gitOutT(t, root, "rev-parse", "HEAD"); head != commit {
		t.Errorf("HEAD = %s, want %s", head, commit)
	}
	if files := gitOutT(t, root, "show", "--name-only", "--format=", "HEAD"); files != "widget_test.go" {
		t.Errorf("commit files = %q, want only widget_test.go", files)
	}
	if staged := gitOutT(t, root, "diff", "--cached", "--name-only"); staged != "widget.go" {
		t.Errorf("still staged = %q, want widget.go", staged)
	}
	if msg := gitOutT(t, root, "log", "-1", "--format=%B"); msg != "Pin Widget with a test" {
		t.Errorf("message = %q", msg)
	}
	got, err := os.ReadFile(filepath.Join(root, "widget.go"))
	if err != nil || string(got) != edited {
		t.Errorf("working file = %q (%v), want the unstaged edit kept", got, err)
	}
}

// A blank message falls back to the plan's own.
// Serial: captures the process-wide os.Stderr.
func TestApplySplit_ABlankMessageUsesThePlansDefault(t *testing.T) {
	root := splitMixedRepo(t)
	var plan SplitPlan
	captureStderr(t, func() { plan, _ = PlanSplit(root, splitGreenRun) })

	if _, err := ApplySplit(root, plan, "  "); err != nil {
		t.Fatalf("ApplySplit: %v", err)
	}

	msg := gitOutT(t, root, "log", "-1", "--format=%B")
	if !strings.HasPrefix(msg, strings.SplitN(plan.Message, "\n", 2)[0]) {
		t.Errorf("message = %q, want the plan's default %q", msg, plan.Message)
	}
}

// An empty plan is refused rather than committing nothing.
func TestApplySplit_RefusesAPlanWithNoTests(t *testing.T) {
	root := makeGoRepo(t)
	head := gitOutT(t, root, "rev-parse", "HEAD")

	if _, err := ApplySplit(root, SplitPlan{}, "msg"); err == nil {
		t.Fatal("an empty plan must be refused")
	}
	if got := gitOutT(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
}
