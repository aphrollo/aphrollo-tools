package precommit

import (
	"strings"
	"testing"
	"time"
)

// greenLabel printed "green (0 tests — nothing to run)" for ANY output that
// contained "no tests to run", before it ever looked at the outcome. Two
// false lines came out of it: a Go test edit whose file has no test yet was
// CLASSIFIED writing-test and PRINTED green, and a multi-package Go run that
// passed real tests beside one package with "[no tests to run]" printed
// "0 tests" over tests that ran. The printed label follows the outcome.

// TestPostEdit_GoTestFileWithNoTestYet_PrintsWritingTestNeverGreen is the
// first: the line must say what the run was classified as, and a run that
// tested nothing must never say green.
func TestPostEdit_GoTestFileWithNoTestYet_PrintsWritingTestNeverGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkGoModule(t)
	write(t, root, "internal/proc/proc_test.go", "package proc\n")
	got := PostEdit(postPayload("Edit", root+"/internal/proc/proc_test.go"),
		fakeRun(true, "testing: warning: no tests to run\nok  \texample.com/m/internal/proc\t0.002s [no tests to run]\n"))

	if strings.Contains(got, "green") {
		t.Fatalf("a run that tested nothing must never print green, got: %s", got)
	}
	if !strings.Contains(got, "→ "+string(WritingTest)+" (0 tests ran") {
		t.Fatalf("want the writing-test label the run was classified as, got: %s", got)
	}
}

// TestMechResultLine_GoRunWithAnEmptyPackagePrintsItsRealCount is the
// second, at the commit and merge gates: one package ran a test, one ran
// none, and the line reports the test that ran rather than "0 tests".
func TestMechResultLine_GoRunWithAnEmptyPackagePrintsItsRealCount(t *testing.T) {
	out := "=== RUN   TestOneIsOne\n--- PASS: TestOneIsOne (0.00s)\nPASS\n" +
		"ok  \texample.com/m/internal/a\t0.004s\n" +
		"testing: warning: no tests to run\nok  \texample.com/m/internal/b\t0.002s [no tests to run]\n"
	line := mechResultLine("precommit", "mechanical", Runner{Cmd: "go", Args: []string{"test", "./internal/a", "./internal/b"}},
		"D:/repo", SuiteResult{Passed: true, Output: out, Duration: 900 * time.Millisecond})

	if strings.Contains(line, "0 tests") {
		t.Fatalf("a run that passed a real test must not print 0 tests, got: %s", line)
	}
	if !strings.Contains(line, "green (1 passed") {
		t.Fatalf("want green with the one test that ran, got: %s", line)
	}
}
