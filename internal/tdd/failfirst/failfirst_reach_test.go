package failfirst

import (
	"strings"
	"testing"
)

// A failed proof counts as reaching the test when it names a failing test
// or points into a staged test file; npx refusing to start the tool, or a
// config that would not load, does neither.
func TestFailureReachedTests_OnlyAFailureInsideTheTestCounts(t *testing.T) {
	tests := []string{"src/lib/caps.test.ts"}
	cases := []struct {
		name, output string
		want         bool
	}{
		{"a failing vitest case", " × caps > has three 3ms\n", true},
		{"an import error in the test file", " FAIL  src/lib/caps.test.ts [ src/lib/caps.test.ts ]\nError: Failed to resolve import \"./next\"\n", true},
		{"npx never started the tool", "npm error npx canceled due to missing packages and no YES option: [\"vitest@3.2.7\"]\n", false},
		{"the config did not load", "failed to load config from /wt/vitest.config.ts\nError: Cannot find module 'vitest/config'\n", false},
	}
	for _, tc := range cases {
		if got := failureReachedTests(tc.output, tests); got != tc.want {
			t.Errorf("%s: reached = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !failureReachedTests("./widget_test.go:6:5: undefined: Widget\n", []string{"pkg/widget_test.go"}) {
		t.Error("a Go compile error in the staged test is the red a test-first commit shows at HEAD")
	}
}

// The note under the verdict quotes the first line the run printed, not a
// blank one ahead of it.
func TestNotReachedNote_QuotesTheRunsFirstPrintedLine(t *testing.T) {
	got := notReachedNote("\n  \nnpm error npx canceled\nmore\n")
	if !strings.Contains(got, "proved nothing either way: npm error npx canceled\n") {
		t.Fatalf("note = %q", got)
	}
}
