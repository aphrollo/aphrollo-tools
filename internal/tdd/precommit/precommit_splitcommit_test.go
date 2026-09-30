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

// Serial: sets the process-wide env vars GOTMPDIR and CLAUDE_CONFIG_DIR (splitMixedRepo).
func TestPlanSplit_NamesTheGreenTestsAndWhatStaysStaged(t *testing.T) {
	root := splitMixedRepo(t)

	var plan SplitPlan
	var err error
	captureGate(t, func() { plan, err = PlanSplit(root, splitGreenRun) })

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
// Serial: sets the process-wide env vars GOTMPDIR and CLAUDE_CONFIG_DIR (splitMixedRepo).
func TestPlanSplit_ChangesNothing(t *testing.T) {
	root := splitMixedRepo(t)
	head := gitOutT(t, root, "rev-parse", "HEAD")
	index := gitOutT(t, root, "write-tree")

	captureGate(t, func() { _, _ = PlanSplit(root, splitGreenRun) })

	if got := gitOutT(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
	if got := gitOutT(t, root, "write-tree"); got != index {
		t.Errorf("index changed: %s -> %s", index, got)
	}
}

// Tests that go RED at HEAD are what the gate wants: there is nothing to split.
// Serial: sets the process-wide env vars GOTMPDIR and CLAUDE_CONFIG_DIR (splitMixedRepo).
func TestPlanSplit_ATestRedAtHeadHasNothingToSplit(t *testing.T) {
	root := splitMixedRepo(t)

	var plan SplitPlan
	var err error
	captureGate(t, func() { plan, err = PlanSplit(root, splitRedRun) })

	if err != nil {
		t.Fatalf("PlanSplit: %v", err)
	}
	if len(plan.Tests) != 0 {
		t.Errorf("Tests = %v, want none for a red test", plan.Tests)
	}
}

// Nothing staged is nothing to split, and it is not an error.
func TestPlanSplit_NothingStagedHasNothingToSplit(t *testing.T) {
	t.Parallel()
	root := makeGoRepo(t)

	plan, err := PlanSplit(root, splitGreenRun)

	if err != nil || len(plan.Tests) != 0 {
		t.Fatalf("PlanSplit = %+v, %v; want an empty plan", plan, err)
	}
}

// Applying the plan commits the tests alone, keeps the rest staged, and
// leaves every file of the working tree as it was.
// Serial: sets the process-wide env vars GOTMPDIR and CLAUDE_CONFIG_DIR (splitMixedRepo).
func TestApplySplit_CommitsTheTestsAloneAndKeepsTheRestStaged(t *testing.T) {
	root := splitMixedRepo(t)
	edited := "package m\n\nfunc Widget() int { return 1 } // edited after staging\n"
	write(t, root, "widget.go", edited)
	var plan SplitPlan
	captureGate(t, func() { plan, _ = PlanSplit(root, splitGreenRun) })

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
// Serial: sets the process-wide env vars GOTMPDIR and CLAUDE_CONFIG_DIR (splitMixedRepo).
func TestApplySplit_ABlankMessageUsesThePlansDefault(t *testing.T) {
	root := splitMixedRepo(t)
	var plan SplitPlan
	captureGate(t, func() { plan, _ = PlanSplit(root, splitGreenRun) })

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
	t.Parallel()
	root := makeGoRepo(t)
	head := gitOutT(t, root, "rev-parse", "HEAD")

	if _, err := ApplySplit(root, SplitPlan{}, "msg"); err == nil {
		t.Fatal("an empty plan must be refused")
	}
	if got := gitOutT(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
}

func TestSplitMessage_NamesTheTestsWhenItKnowsThem(t *testing.T) {
	t.Parallel()
	got := splitMessage([]string{"TestA", "TestB"}, []string{"a_test.go"})

	want := "Add tests that pass against the current code\n\nTests: TestA, TestB"
	if got != want {
		t.Fatalf("splitMessage = %q, want %q", got, want)
	}
}

// With no test names (a runner whose filter names none) the files carry it.
func TestSplitMessage_FallsBackToTheFilesWhenNoNameIsKnown(t *testing.T) {
	t.Parallel()
	got := splitMessage(nil, []string{"a_test.go", "b_test.go"})

	want := "Add tests that pass against the current code\n\nFiles: a_test.go, b_test.go"
	if got != want {
		t.Fatalf("splitMessage = %q, want %q", got, want)
	}
}

func TestAppendNew_SkipsWhatTheListAlreadyHolds(t *testing.T) {
	t.Parallel()
	got := appendNew([]string{"a", "b"}, "b", "c", "a")

	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("appendNew = %v, want [a b c]", got)
	}
}

// The test-only commit is written without a hook, so ApplySplit judges its
// tree itself: a staged test that breaks a ratchet law is refused and HEAD
// stays put.
// Serial: sets the process-wide env vars GOTMPDIR and CLAUDE_CONFIG_DIR (tddtest.VerdictWordTmp).
func TestApplySplit_RefusesATestTreeThatBreaksARatchetLaw(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, ".ratchet/laws/no-forbidden.toml", "name = \"no-forbidden\"\ndescription = \"no FORBIDDEN in tests\"\nseverity = \"deny\"\n\n[scope]\ninclude = [\"**/*_test.go\"]\n\n[matcher]\nkind = \"regex-absent\"\npattern = \"FORBIDDEN\"\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "law")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\n// FORBIDDEN\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	head := gitOutT(t, root, "rev-parse", "HEAD")
	var plan SplitPlan
	captureStderr(t, func() { plan, _ = PlanSplit(root, splitGreenRun) })
	if len(plan.Tests) == 0 {
		t.Fatal("setup: the plan should offer the test")
	}

	var err error
	captureStderr(t, func() { _, err = ApplySplit(root, plan, "Pin Widget") })

	if err == nil || !strings.Contains(err.Error(), "no-forbidden") {
		t.Fatalf("ApplySplit err = %v, want a ratchet refusal naming no-forbidden", err)
	}
	if got := gitOutT(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s despite the refusal", got)
	}
}
