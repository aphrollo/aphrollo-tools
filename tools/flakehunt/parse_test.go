package main

import (
	"os"
	"strings"
	"testing"
)

// openFixture opens a recorded `go test -json` transcript under testdata/,
// captured by literally running `go test -shuffle=on -count=N -json` against
// a throwaway module — real event shapes, not hand-typed guesses.
func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestParse_AllPassingReportsNoFailures(t *testing.T) {
	res, err := Parse(openFixture(t, "allpass.jsonl"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("all-passing fixture reported failures: %+v", res.Failures)
	}
	if len(res.BuildFailures) != 0 {
		t.Fatalf("all-passing fixture reported build failures: %v", res.BuildFailures)
	}
}

// mixed.jsonl is `-shuffle=on -count=3` across two packages: pkga passes
// throughout, pkgb carries a test that fails only on its 2nd of 3 calls
// (TestFlaky) and a subtest that fails every time (TestGroup/case2).
func TestParse_FlakyTestReportsOneFailureWithSeedAndExcerpt(t *testing.T) {
	res, err := Parse(openFixture(t, "mixed.jsonl"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var flaky *Failure
	for i := range res.Failures {
		if res.Failures[i].Test == "TestFlaky" {
			flaky = &res.Failures[i]
		}
	}
	if flaky == nil {
		t.Fatalf("TestFlaky missing from failures: %+v", res.Failures)
	}
	if flaky.Package != "fixturemod/pkgb" {
		t.Fatalf("Package = %q, want fixturemod/pkgb", flaky.Package)
	}
	if flaky.Seed == "" {
		t.Fatal("Seed is empty — the package's -test.shuffle line was not captured")
	}
	if !strings.Contains(flaky.Excerpt, "boom: shared temp dir collision") {
		t.Fatalf("Excerpt = %q, want it to contain the failing t.Fatal text", flaky.Excerpt)
	}
}

// A failing subtest must fold up to its top-level test (the unit
// `-run '^Name$'` reproduces), and the excerpt kept must be the subtest's own
// — never the parent's contentless "--- FAIL: TestGroup" echo emitted right
// after it.
func TestParse_FailingSubtestFoldsToTopLevelTestKeepingItsOwnExcerpt(t *testing.T) {
	res, err := Parse(openFixture(t, "mixed.jsonl"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var group *Failure
	for i := range res.Failures {
		if res.Failures[i].Test == "TestGroup" {
			group = &res.Failures[i]
		}
	}
	if group == nil {
		t.Fatalf("TestGroup missing from failures (subtest did not fold up): %+v", res.Failures)
	}
	if !strings.Contains(group.Excerpt, "boom: subtest fails every time") {
		t.Fatalf("Excerpt = %q, want the subtest's own failure text, not the parent's bare echo", group.Excerpt)
	}
	for _, f := range res.Failures {
		if f.Test == "TestGroup/case2" {
			t.Fatalf("subtest %q reported as its own failure — it must fold into its top-level test", f.Test)
		}
	}
}

func TestParse_BuildFailureReportedSeparatelyFromTestFailures(t *testing.T) {
	res, err := Parse(openFixture(t, "buildfail.jsonl"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("a build failure filed as a test failure: %+v", res.Failures)
	}
	if len(res.BuildFailures) != 1 || res.BuildFailures[0] != "fixturemod/pkgbuildfail" {
		t.Fatalf("BuildFailures = %v, want exactly [fixturemod/pkgbuildfail]", res.BuildFailures)
	}
}
