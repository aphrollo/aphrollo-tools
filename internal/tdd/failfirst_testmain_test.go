package tdd

import (
	"strings"
	"testing"
	"time"
)

// A package's TestMain is its test binary's entry point, not a test: `go test
// -run '^(TestMain)$'` selects nothing, so a proof built from it executes
// zero tests and is refused as vacuous. The split's L2 carve-out hit exactly
// this: its follow-on commit staged only lock/main_test.go, whose one
// declaration is TestMain, beside non-test edits, and the commit was
// vacuous-rejected. A commit whose only added test declaration is a TestMain
// has no new test to prove, the same as a commit that adds none.
func TestFailFirstStage_ATestMainAloneIsNoNewTestToProve(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "lock/lock.go", "package lock\n\nfunc Held() bool { return false }\n")
	write(t, root, "lock/lock_test.go",
		"package lock\n\nimport \"testing\"\n\nfunc TestHeld_FalseByDefault(t *testing.T) {\n\tif Held() {\n\t\tt.Fatal(\"held\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "lock package")

	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	write(t, root, "lock/main_test.go",
		"package lock\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n")
	gitDo(t, root, "add", ".")

	ran := 0
	run := func(Runner, string) SuiteResult {
		ran++
		return SuiteResult{Passed: true}
	}
	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"lock/main_test.go"}, []string{"widget.go"}, run)
	})
	if res.Blocked {
		t.Fatalf("a staged TestMain alone was refused: %s", res.Message)
	}
	if ran != 0 || strings.Contains(stderr, "vacuous") {
		t.Fatalf("fail-first ran %d proof(s) for a TestMain, which declares no test; stderr:\n%s", ran, stderr)
	}
}

// The -run filter names the tests a staged file declares, and TestMain is not
// one: a file declaring TestMain beside a real test narrows to the real test
// alone, and a file declaring only TestMain yields no name, so the proof keeps
// package scope instead of an empty filter.
func TestNarrowFailFirstTests_GoFilterLeavesTestMainOut(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.21\n")
	write(t, root, "internal/x/x.go", "package x\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "internal/x/main_test.go",
		"package x\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) { m.Run() }\n\nfunc TestAlpha_widgetIsOne(t *testing.T) {}\n")
	write(t, root, "internal/y/y.go", "package y\n")
	write(t, root, "internal/y/main_test.go",
		"package y\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) { m.Run() }\n")

	goRunner := Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(goRunner, root, []string{"internal/x/main_test.go"})
	if want := "^(TestAlpha_widgetIsOne)$"; strings.Join(got.Args, " ") != "test ./internal/x -run "+want {
		t.Errorf("narrowed argv = %q, want the filter %s", got.Args, want)
	}
	got = narrowFailFirstTests(goRunner, root, []string{"internal/y/main_test.go"})
	if strings.Join(got.Args, " ") != "test ./internal/y" {
		t.Errorf("a file declaring only TestMain narrowed to %q, want package scope `test ./internal/y`", got.Args)
	}
}
