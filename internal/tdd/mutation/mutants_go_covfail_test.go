package mutation

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// gremlins' coverage gather (a bare `go test -cover ./...`, mutants_go.go)
// prints go test's own failure lines when the module is red at HEAD, and
// this is the parser that reads a failing test's name back out of them.
func TestCoverageRunFailedTests_NamesFailingTestsFromGoTestOutput(t *testing.T) {
	output := "=== RUN   TestFoo\n" +
		"--- FAIL: TestFoo (0.01s)\n" +
		"    foo_test.go:9: boom\n" +
		"=== RUN   TestBar\n" +
		"--- FAIL: TestBar (0.00s)\n" +
		"FAIL\n" +
		"FAIL\tgithub.com/aphrollo/aphrollo-tools/internal/x\t0.02s\n"

	got := coverageRunFailedTests(output)

	want := []string{"TestBar", "TestFoo"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("coverageRunFailedTests = %v, want %v (sorted, de-duplicated)", got, want)
	}
}

// A duplicate --- FAIL line (a retried subtest, or the same test failing in
// two packages) must name the test once, not once per line.
func TestCoverageRunFailedTests_DeduplicatesTheSameTestName(t *testing.T) {
	output := "--- FAIL: TestFoo (0.01s)\n--- FAIL: TestFoo (0.02s)\n"

	got := coverageRunFailedTests(output)

	if len(got) != 1 || got[0] != "TestFoo" {
		t.Fatalf("coverageRunFailedTests = %v, want [TestFoo] once", got)
	}
}

// Output with no "--- FAIL:" line at all (a build failure, a killed process)
// names nothing: an empty "coverage run failed ()" is less honest than the
// generic no-verdict refusal it would otherwise replace.
func TestCoverageRunFailedTests_EmptyWhenOutputNamesNoTest(t *testing.T) {
	if got := coverageRunFailedTests("gremlins: error computing coverage: exit status 1\n"); got != nil {
		t.Fatalf("coverageRunFailedTests = %v, want nil for output naming no failing test", got)
	}
}

// The condition under test: a coverage gather that failed with named tests is
// NOT MEASURED, never Refused — gremlins never reached the tree, so there is
// nothing here for a merge to be blocked over, unlike the generic no-verdict
// refusal below it.
func TestGoCoverageNoVerdict_NamesTheFailingTestsAsNotMeasured(t *testing.T) {
	root, _ := measureFixture(t, laneSource)
	before := snapshotWorktree(root)
	output := "--- FAIL: TestWidget (0.00s)\nFAIL\n"

	v := goCoverageNoVerdict(root, "logdir", 1, errors.New("exit status 1"), before, output, io.Discard)

	if v.Refused {
		t.Fatalf("verdict = %+v, want NOT refused — a coverage-gather failure blocks nothing", v)
	}
	if v.NotMeasured == "" {
		t.Fatalf("verdict = %+v, want NotMeasured carrying the coverage-run diagnosis", v)
	}
	if !strings.Contains(v.NotMeasured, "coverage run failed (TestWidget)") {
		t.Fatalf("NotMeasured = %q, want it to say %q", v.NotMeasured, "coverage run failed (TestWidget)")
	}
	if !strings.Contains(v.Message, "coverage run failed (TestWidget)") {
		t.Fatalf("Message = %q, want the same diagnosis", v.Message)
	}
}

// Tree damage outranks a coverage diagnosis, even when the run's own output
// also names a failing test: a mutation still sitting in the source after a
// killed run is a fact about the REPOSITORY, and merging it merges a mutant
// regardless of what else the log says about why the run died.
func TestGoCoverageNoVerdict_TreeChangedWinsOverACoverageDiagnosis(t *testing.T) {
	root, _ := measureFixture(t, laneSource)
	before := snapshotWorktree(root)
	write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b + 1 }\n")
	output := "--- FAIL: TestWidget (0.00s)\nFAIL\n"

	v := goCoverageNoVerdict(root, "logdir", 1, errors.New("exit status 1"), before, output, io.Discard)

	if !v.Refused {
		t.Fatalf("verdict = %+v, want Refused — the run left the tree changed", v)
	}
	if v.NotMeasured != "" {
		t.Fatalf("verdict = %+v, want no coverage diagnosis attached — tree damage takes priority", v)
	}
	if !strings.Contains(v.Message, "the run left the working tree changed") {
		t.Fatalf("Message = %q, want the tree-changed refusal, not the coverage diagnosis", v.Message)
	}
}

// Output that names no failing test still falls back to the generic
// no-verdict refusal: an unexplained gremlins failure is not silently waved
// through as an honest gap just because this file could not name a cause.
func TestGoCoverageNoVerdict_FallsBackToNoVerdictWhenNoTestIsNamed(t *testing.T) {
	root, _ := measureFixture(t, laneSource)
	before := snapshotWorktree(root)

	v := goCoverageNoVerdict(root, "logdir", 1, errors.New("exit status 1"), before, "gremlins: something broke\n", io.Discard)

	if !v.Refused {
		t.Fatalf("verdict = %+v, want the generic no-verdict refusal when no test is named", v)
	}
	if v.NotMeasured != "" {
		t.Fatalf("verdict = %+v, want no coverage diagnosis attached", v)
	}
}
