package suite

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func relatedPaths(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("src/components/ui/Widget%03d.test.tsx", i))
	}
	return out
}

// runOnce records the runners the executor starts and answers a pass.
func runOnce(started *[]Runner) SuiteRunner {
	return func(b Runner, _ string) SuiteResult {
		*started = append(*started, b)
		return SuiteResult{Passed: true, Output: "ok\n"}
	}
}

// TestRunBatched_RelatedRunnerPastTheBudgetRunsTheFullSuite is issue #1008:
// the merge gate started `vitest related <330 paths> --run`, a line no
// Windows shell starts. Whatever built the runner, the executor that starts
// it holds the line to the budget: past it, the related run becomes the
// tool's full suite, which covers every related test.
func TestRunBatched_RelatedRunnerPastTheBudgetRunsTheFullSuite(t *testing.T) {
	entry := "/repo/frontend/node_modules/vitest/vitest.mjs"
	jestEntry := "/repo/frontend/node_modules/jest/bin/jest.js"
	files := relatedPaths(330)
	cases := []struct {
		name string
		r    Runner
		want string
	}{
		{"npx vitest", Runner{Cmd: "npx", Args: append(append([]string{"vitest", "related"}, files...), "--run")}, "npx vitest run"},
		{"node vitest entry", Runner{Cmd: "node", Args: append(append([]string{entry, "related"}, files...), "--run")}, "node " + entry + " run"},
		{"npx jest", Runner{Cmd: "npx", Args: append([]string{"jest", "--findRelatedTests"}, files...)}, "npx jest"},
		{"node jest entry", Runner{Cmd: "node", Args: append([]string{jestEntry, "--findRelatedTests"}, files...)}, "node " + jestEntry},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var started []Runner
			out := tddtest.CaptureStderr(t, func() { runBatched(c.r, "root", time.Minute, 6000, runOnce(&started)) })
			if len(started) != 1 || commandLine(started[0]) != c.want {
				t.Fatalf("started %d runs, first %.100q, want the one full suite %q", len(started), commandLine(started[0]), c.want)
			}
			if !strings.Contains(out, fmt.Sprintf("%d chars", len(commandLine(c.r)))) || !strings.Contains(out, "6000-char") || !strings.Contains(out, c.want) {
				t.Fatalf("the fallback must say what decided it, got %q", out)
			}
		})
	}
}

// TestRunBatched_RelatedRunnerBudgetIsInclusive pins the edge: a related line
// exactly at the budget starts as built, one character over runs the full
// suite.
func TestRunBatched_RelatedRunnerBudgetIsInclusive(t *testing.T) {
	r := Runner{Cmd: "npx", Args: append(append([]string{"vitest", "related"}, relatedPaths(10)...), "--run")}
	line := len(commandLine(r))
	var started []Runner
	runBatched(r, "root", time.Minute, line, runOnce(&started))
	if len(started) != 1 || commandLine(started[0]) != commandLine(r) {
		t.Fatalf("a line at the budget must start as built, got %.80q", commandLine(started[0]))
	}
	started = nil
	tddtest.CaptureStderr(t, func() { runBatched(r, "root", time.Minute, line-1, runOnce(&started)) })
	if len(started) != 1 || commandLine(started[0]) != "npx vitest run" {
		t.Fatalf("a line one over the budget must run the full suite, got %.80q", commandLine(started[0]))
	}
}

// TestRelatedFullSuite_LeavesEveryOtherRunnerAlone pins what is not a related
// run: a plain full-suite run and another tool stay as they are.
func TestRelatedFullSuite_LeavesEveryOtherRunnerAlone(t *testing.T) {
	for _, r := range []Runner{
		{Cmd: "npx", Args: []string{"vitest", "run"}},
		{Cmd: "npx", Args: []string{"jest"}},
		{Cmd: "go", Args: []string{"test", "related", "./a"}},
		{Cmd: "npx"},
		{Cmd: "npx", Args: []string{"vitest"}},
	} {
		if _, ok := relatedFullSuite(r); ok {
			t.Errorf("%s is not a related run, and must not be rewritten", commandLine(r))
		}
	}
}
