package suite

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// changedTestFiles is a large PR's changed set: n test files under a deep,
// realistic monorepo path, about 80 characters each.
func changedTestFiles(n int) []string {
	files := make([]string, 0, n)
	for i := range n {
		files = append(files, fmt.Sprintf("packages/storefront-checkout/src/components/payment-methods/Widget%03d.test.tsx", i))
	}
	return files
}

// commandLine is the line a runner's argv makes, its words joined by single
// spaces: the string a Windows process is started with.
func commandLine(r Runner) string {
	return strings.Join(append([]string{r.Cmd}, r.Args...), " ")
}

// TestNarrowToStaged_PastTheArgvBudgetRunsTheFullSuite is issue #951: the
// merge gate built `vitest related <every changed file>`, and on a large PR
// that line passes the Windows command-line limit, so the suite never
// starts. Past the budget each runner that puts changed paths into its argv
// runs its full suite instead, which covers every related test.
func TestNarrowToStaged_PastTheArgvBudgetRunsTheFullSuite(t *testing.T) {
	stubGoWorkspaceGraphError(t, errors.New("go list: no such tool"))
	goFiles := make([]string, 0, 300)
	for i := range 300 {
		goFiles = append(goFiles, fmt.Sprintf("internal/platform/services/billing/adapters/provider%03d/client.go", i))
	}
	cases := []struct {
		name  string
		base  Runner
		files []string
	}{
		{"vitest related", Runner{Cmd: "npx", Args: []string{"vitest", "run"}}, changedTestFiles(200)},
		{"jest --findRelatedTests", Runner{Cmd: "npx", Args: []string{"jest"}}, changedTestFiles(200)},
		{"go test packages", Runner{Cmd: "go", Args: []string{"test", "./..."}}, goFiles},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range goFiles {
				write(t, root, f, "package client\n")
			}
			var got Runner
			var ok bool
			tddtest.CaptureStderr(t, func() { got, ok = narrowToStaged(c.base, root, c.files) })
			if !ok {
				t.Fatal("narrowToStaged declined; the full-suite fallback must still report a runner to run")
			}
			if commandLine(got) != commandLine(c.base) {
				t.Fatalf("got %d-char command %.120q…, want the full suite %q", len(commandLine(got)), commandLine(got), commandLine(c.base))
			}
		})
	}
}

// TestNarrowToStaged_ArgvBudgetIsInclusive pins the edge: a related-tests
// line exactly at the budget still runs narrowed, one character more runs
// the full suite.
func TestNarrowToStaged_ArgvBudgetIsInclusive(t *testing.T) {
	base := Runner{Cmd: "npx", Args: []string{"vitest", "run"}}
	// "npx vitest related <files> --run": the fixed words take 23 chars
	// with their spaces; one file of length L adds L+1.
	fixed := len("npx vitest related --run")
	files := changedTestFiles(60)
	used := fixed
	for _, f := range files {
		used += len(f) + 1
	}
	pad := stagedArgvBudget - used
	if pad < 1 {
		t.Fatalf("fixture too long for the budget: %d chars before padding", used)
	}
	last := strings.Repeat("x", pad-1)
	atBudget := append(slices.Clone(files), last)

	root := t.TempDir()
	got, _ := narrowToStaged(base, root, atBudget)
	if n := len(commandLine(got)); n != stagedArgvBudget {
		t.Fatalf("fixture line is %d chars, want exactly the %d-char budget", n, stagedArgvBudget)
	}
	if got.Args[1] != "related" {
		t.Fatalf("a line exactly at the budget must stay narrowed, got %q", commandLine(got)[:40])
	}

	overBudget := append(slices.Clone(files), last+"x")
	tddtest.CaptureStderr(t, func() { got, _ = narrowToStaged(base, root, overBudget) })
	if commandLine(got) != commandLine(base) {
		t.Fatalf("one char past the budget must run the full suite, got %d chars", len(commandLine(got)))
	}
}

// TestNarrowToStaged_ArgvBudgetFallbackSaysSo pins the line the fallback
// prints: which tree, how many changed files, the size it would have been
// against the budget, and the full-suite command that runs instead. A merge
// that quietly ran every test would read as a slow gate, not a decision.
func TestNarrowToStaged_ArgvBudgetFallbackSaysSo(t *testing.T) {
	base := Runner{Cmd: "npx", Args: []string{"vitest", "run"}}
	root := t.TempDir()
	files := changedTestFiles(200)
	want := len(commandLine(Runner{Cmd: "npx", Args: append(append([]string{"vitest", "related"}, files...), "--run")}))

	out := tddtest.CaptureStderr(t, func() { narrowToStaged(base, root, files) })
	for _, part := range []string{root, "200 changed files", fmt.Sprintf("%d chars", want), fmt.Sprintf("%d-char", stagedArgvBudget), "npx vitest run"} {
		if !strings.Contains(out, part) {
			t.Fatalf("fallback line missing %q:\n%s", part, out)
		}
	}
	if again := tddtest.CaptureStderr(t, func() { narrowToStaged(base, root, files) }); again != "" {
		t.Fatalf("the same fallback in one gate run must be said once, got a second line:\n%s", again)
	}
}
