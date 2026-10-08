package failfirst

import (
	"reflect"
	"strings"
	"testing"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func TestGoRunNames_ReadsTheStagedNamesOutOfTheProofsRunFilter(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"separate value", []string{"test", "./p", "-run", "^(TestA|TestB)$"}, []string{"TestA", "TestB"}},
		{"equals form", []string{"test", "-run=^(TestA)$"}, []string{"TestA"}},
		{"no filter", []string{"test", "./..."}, nil},
		{"flag without a value", []string{"test", "-run"}, nil},
		{"unanchored filter", []string{"test", "-run", "TestA"}, nil},
		{"missing tail anchor", []string{"test", "-run", "^(TestA)"}, nil},
		{"missing head anchor", []string{"test", "-run", "(TestA)$"}, nil},
		{"empty group", []string{"test", "-run", "^()$"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := goRunNames(tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("goRunNames(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestSplitAdvice_NamesTheTestsTheFilesAndTheCommands(t *testing.T) {
	got := splitAdvice([]string{"a_test.go", "b_test.go"}, []string{"TestA", "TestB"}, nil)
	for _, want := range []string{
		"Tests that pass at HEAD: TestA, TestB",
		"Test files: a_test.go, b_test.go",
		"aphrollo gate split-commit --dry",
		"aphrollo gate split-commit -m \"<what the tests pin>\"",
		"git commit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("advice lacks %q:\n%s", want, got)
		}
	}
}

func TestSplitAdvice_WithoutNamesStillNamesTheFilesAndCommands(t *testing.T) {
	got := splitAdvice([]string{"a_test.go"}, nil, nil)
	if strings.Contains(got, "Tests that pass at HEAD") {
		t.Errorf("no names known, yet the advice lists tests:\n%s", got)
	}
	for _, want := range []string{"Test files: a_test.go", "aphrollo gate split-commit"} {
		if !strings.Contains(got, want) {
			t.Errorf("advice lacks %q:\n%s", want, got)
		}
	}
}

// passedAtHeadJSON is the run of one Go test that ran and passed.
const passedAtHeadJSON = `{"Action":"run","Package":"example.com/m","Test":"TestWidget"}
{"Action":"pass","Package":"example.com/m","Test":"TestWidget","Elapsed":0}
{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

func greenAtHeadRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	return root
}

// The refusal of a commit whose tests already pass at HEAD names those tests
// and prints the commands that split the commit.
// Serial: captures the process-wide os.Stderr.
func TestFailFirstStage_ViolationNamesTheGreenTestsAndTheSplitCommands(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := greenAtHeadRepo(t)
	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: passedAtHeadJSON}
	}

	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)
	})

	if !res.Blocked {
		t.Fatal("tests that pass at HEAD must be refused")
	}
	for _, want := range []string{
		"PASS against the pre-edit code",
		"Tests that pass at HEAD: TestWidget",
		"Test files: widget_test.go",
		"aphrollo gate split-commit -m",
	} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal lacks %q:\n%s", want, res.Message)
		}
	}
}

// ProveGreenAtHead answers with the tests, their files and the staged data
// they read exactly when the commit gate would have refused the commit.
// Serial: captures the process-wide os.Stderr.
func TestProveGreenAtHead_ReportsATestThatPassesAtHead(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := greenAtHeadRepo(t)
	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: passedAtHeadJSON}
	}

	got, ok := ProveGreenAtHead(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)

	if !ok {
		t.Fatal("a test that passes at HEAD must be reported green")
	}
	if !reflect.DeepEqual(got.Names, []string{"TestWidget"}) {
		t.Errorf("Names = %v, want [TestWidget]", got.Names)
	}
}

// A test that fails at HEAD is the red the gate wants: nothing to split.
// Serial: captures the process-wide os.Stderr.
func TestProveGreenAtHead_ATestRedAtHeadIsNotGreen(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := greenAtHeadRepo(t)
	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: "--- FAIL: TestWidget (0.00s)\n    widget_test.go:7: no\nFAIL\n"}
	}

	if _, ok := ProveGreenAtHead(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run); ok {
		t.Fatal("a test that fails at HEAD is red-proven, not green")
	}
}

// A commit with no staged source has nothing to split off from.
func TestProveGreenAtHead_NoStagedSourceMeansNothingToSplit(t *testing.T) {
	root := greenAtHeadRepo(t)
	called := false
	run := func(Runner, string) SuiteResult { called = true; return SuiteResult{Passed: true} }

	if _, ok := ProveGreenAtHead(root, root, []string{"widget_test.go"}, nil, run); ok {
		t.Fatal("tests alone are already a test-only commit")
	}
	if called {
		t.Fatal("no proof should run when the gate would not run one")
	}
}

// The data a staged test reads travels with it: a test-only commit without
// its fixture would be red.
// Serial: captures the process-wide os.Stderr.
func TestProveGreenAtHead_CarriesTheStagedDataTheTestsRead(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "testdata/w.txt", "1\n")
	write(t, root, "widget_test.go", "package m\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestWidget(t *testing.T) {\n\tif _, err := os.ReadFile(\"testdata/w.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: passedAtHeadJSON}
	}

	got, ok := ProveGreenAtHead(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)

	if !ok {
		t.Fatal("a test that passes at HEAD must be reported green")
	}
	if !reflect.DeepEqual(got.Inputs, []string{"testdata/w.txt"}) {
		t.Errorf("Inputs = %v, want [testdata/w.txt]", got.Inputs)
	}
}
