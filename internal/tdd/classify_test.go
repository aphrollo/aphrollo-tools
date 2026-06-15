package tdd

import (
	"reflect"
	"testing"
)

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name        string
		passed      bool
		output      string
		prevFailing []string
		want        Outcome
	}{
		{"clean pass", true, "ok  pkg  0.1s\nPASS", nil, Green},
		{"pass with warnings", true, "PASS\nwarning: unused import", nil, GreenWithWarnings},
		// A passing test edit is GREEN, not a tautology guess — post-edit can't
		// know if the impl pre-existed (backfill/split/refactor all pass).
		{"passing test edit is green", true, "ok\nPASS", nil, Green},
		// The 0-tests false-GREEN fix, across runners.
		{"go no tests", true, "testing: warning: no tests to run\nPASS", nil, WritingTest},
		{"pytest 0 collected", true, "collected 0 items", nil, WritingTest},
		{"vitest 0 tests", true, "Test Files  no tests\n0 tests", nil, WritingTest},
		// Failures.
		{"missing impl is clean red", false, "./x_test.go:9: undefined: NewWidget", nil, RedMissingImpl},
		{"python import is bogus", false, "ERROR collecting tests/x.py\nModuleNotFoundError: no module named 'q'", nil, RedBogus},
		{"syntax error is bogus", false, "SyntaxError: invalid syntax", nil, RedBogus},
		{"plain assertion failure", false, "--- FAIL: TestThing\n  want 1 got 2", nil, Red},
		// Pre-existing failure → no-delta (don't nag).
		{"no new failures", false, "--- FAIL: TestOld\n want x", []string{"TestOld"}, NoDelta},
		{"a new failure breaks no-delta", false, "--- FAIL: TestNew\n want x", []string{"TestOld"}, Red},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyOutcome(c.passed, c.output, c.prevFailing); got != c.want {
				t.Fatalf("ClassifyOutcome = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExtractFailingTests(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []string
	}{
		{"go", "--- FAIL: TestA (0.00s)\n--- FAIL: TestB (0.00s)", []string{"TestA", "TestB"}},
		{"pytest", "FAILED tests/x.py::test_foo\ntests/x.py::test_bar FAILED", []string{"tests/x.py::test_bar", "tests/x.py::test_foo"}},
		{"cargo", "test mod::it ... FAILED", []string{"mod::it"}},
		// vitest name with parentheses must survive the duration strip.
		{"vitest parens", "  ✗ should handle (error cases) (12 ms)", []string{"should handle (error cases)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractFailingTests(c.output); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ExtractFailingTests = %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestOutcome_IsRed(t *testing.T) {
	red := []Outcome{RedMissingImpl, RedBogus, Red}
	for _, o := range red {
		if !o.IsRed() {
			t.Errorf("%q should be red", o)
		}
	}
	for _, o := range []Outcome{Green, GreenWithWarnings, WritingTest, NoDelta} {
		if o.IsRed() {
			t.Errorf("%q should not be red", o)
		}
	}
}
