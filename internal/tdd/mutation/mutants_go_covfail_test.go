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

// ratchet: test_removed TestGoCoverageNoVerdict_NamesTheFailingTestsAsNotMeasured: a failing test in the lane's own tree now refuses the measurement (issue #966); TestGoCoverageNoVerdict_RefusesALaneWhoseOwnTestFails pins the new rule.

// A coverage gather that died on the lane's own failing test is a broken
// lane, not an unmeasured one: reported as NOT MEASURED it exited 0 and let
// the PR open and the merge through with no mutation evidence (issue #966).
// The refusal names the failing package and the test.
func TestGoCoverageNoVerdict_RefusesALaneWhoseOwnTestFails(t *testing.T) {
	root, _ := measureFixture(t, laneSource)
	before := snapshotWorktree(root)
	output := "--- FAIL: TestWidget (0.00s)\n    w_test.go:3: no\nFAIL\n" +
		"FAIL\texample.com/lane/widget\t0.004s\n" +
		"\texample.com/lane/other\t\tcoverage: 0.0% of statements\nFAIL\n"

	v := goCoverageNoVerdict(root, "logdir", 1, errors.New("exit status 1"), before, output, io.Discard)

	if !v.Refused {
		t.Fatalf("verdict = %+v, want Refused — the lane's own test failed the coverage gather", v)
	}
	if v.NotMeasured != "" {
		t.Fatalf("NotMeasured = %q, want empty — a broken lane is refused, not waved through as unmeasured", v.NotMeasured)
	}
	for _, want := range []string{"example.com/lane/widget", "TestWidget"} {
		if !strings.Contains(v.Message, want) {
			t.Fatalf("Message = %q, want it to name %q", v.Message, want)
		}
	}
	if strings.Contains(v.Message, "example.com/lane/other") {
		t.Fatalf("Message = %q, names a package that passed", v.Message)
	}
}

// A lane that does not compile never prints a "--- FAIL:" line at all, only
// go test's per-package "[build failed]" line: the refusal still names the
// package, never a bare exit status and a log directory.
func TestGoCoverageNoVerdict_RefusesALaneThatDoesNotBuild(t *testing.T) {
	root, _ := measureFixture(t, laneSource)
	before := snapshotWorktree(root)
	output := "# example.com/lane/widget\nwidget/w.go:2:9: undefined: x\n" +
		"FAIL\texample.com/lane/widget [build failed]\nFAIL\n"

	v := goCoverageNoVerdict(root, "logdir", 1, errors.New("exit status 1"), before, output, io.Discard)

	if !v.Refused || v.NotMeasured != "" {
		t.Fatalf("verdict = %+v, want Refused and not NOT MEASURED", v)
	}
	if !strings.Contains(v.Message, "example.com/lane/widget") {
		t.Fatalf("Message = %q, want it to name the package that does not build", v.Message)
	}
}

// A capture that kept the "--- FAIL:" line but lost go test's per-package
// verdict still blames a test in the lane: it is refused naming that test,
// never sent to the generic no-verdict refusal that names neither.
func TestGoCoverageNoVerdict_RefusesOnAFailingTestWithNoPackageLine(t *testing.T) {
	root, _ := measureFixture(t, laneSource)
	before := snapshotWorktree(root)
	output := "--- FAIL: TestWidget (0.00s)\nFAIL\n"

	v := goCoverageNoVerdict(root, "logdir", 1, errors.New("exit status 1"), before, output, io.Discard)

	if !v.Refused || !strings.Contains(v.Message, "TestWidget") {
		t.Fatalf("verdict = %+v, want Refused naming TestWidget", v)
	}
}

// What the box did to the gather is not the lane's fault: a test binary the
// kernel killed, a full drive, or a fork the box could not make still prints
// go test's per-package FAIL line, and that stays NOT MEASURED — refusing it
// would block a merge over the box rather than the code.
func TestGoCoverageNoVerdict_BoxSideFailureStaysNotMeasured(t *testing.T) {
	for _, cause := range []string{
		"signal: killed",
		"write /tmp/go-build1/b001/x: no space left on device",
		"fork/exec /tmp/go-build1/b001/w.test: cannot allocate memory",
		"fork/exec /tmp/go-build1/b001/w.test: resource temporarily unavailable",
	} {
		t.Run(cause, func(t *testing.T) {
			root, _ := measureFixture(t, laneSource)
			before := snapshotWorktree(root)
			output := cause + "\nFAIL\texample.com/lane/widget\t1.200s\nFAIL\n"

			v := goCoverageNoVerdict(root, "logdir", 1, errors.New("exit status 1"), before, output, io.Discard)

			if v.Refused {
				t.Fatalf("verdict = %+v, want NOT refused — the box broke the gather, not the lane", v)
			}
			if v.NotMeasured == "" {
				t.Fatalf("verdict = %+v, want NotMeasured naming the box-side cause", v)
			}
		})
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
