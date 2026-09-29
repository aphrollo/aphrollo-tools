package precommit

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestEslintArgvs_SplitsALargeChangedSetUnderTheArgvBudget is issue #951's
// eslint half: the check put every changed lintable file on one command
// line. Each ESLint run is compared with the same run at HEAD on its own, so
// the files split into several runs, each line within the budget, together
// linting every file once and in order.
func TestEslintArgvs_SplitsALargeChangedSetUnderTheArgvBudget(t *testing.T) {
	root := t.TempDir()
	var files []string
	for i := range 200 {
		f := fmt.Sprintf("packages/storefront-checkout/src/components/payment-methods/Widget%03d.tsx", i)
		write(t, root, f, "export const x = 1\n")
		files = append(files, f)
	}

	argvs := eslintArgvs(root, files)

	if len(argvs) < 2 {
		t.Fatalf("got %d eslint run(s) for %d files, want the set split across several", len(argvs), len(files))
	}
	var linted []string
	for i, args := range argvs {
		if n := len(strings.Join(args, " ")); n > stagedArgvBudget {
			t.Errorf("run %d is %d chars, past the %d-char budget", i, n, stagedArgvBudget)
		}
		if !slices.Equal(args[:2], []string{"--format", "json"}) {
			t.Fatalf("run %d starts %q, want --format json first", i, args[:2])
		}
		linted = append(linted, args[2:]...)
	}
	if !slices.Equal(linted, files) {
		t.Fatalf("the runs lint %d files, want each of the %d changed files once, in order", len(linted), len(files))
	}
}
