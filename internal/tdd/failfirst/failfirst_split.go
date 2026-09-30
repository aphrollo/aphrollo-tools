package failfirst

import (
	"fmt"
	"strings"
)

// HeadGreen is a fail-first proof that ended in a violation: the staged tests
// PASSED against HEAD, so they need no change to be committed and can go in
// a commit of their own ahead of the implementation.
type HeadGreen struct {
	// Names are the tests that passed, read out of the proof's own run
	// filter; nil for a runner whose filter does not name tests.
	Names []string
	// Inputs is the staged data the tests read (fixtures, testdata): part of
	// the tree the proof ran against, so part of the test-only commit.
	Inputs []string
}

// ProveGreenAtHead runs the same proof failFirstStage runs and reports
// whether it would REFUSE the commit for its tests passing at HEAD. ok is
// false when the proof does not apply (no staged source beside the tests, or
// no new test declaration), when it was inconclusive, and when the tests went
// RED — the outcome the gate wants, with nothing to split.
func ProveGreenAtHead(repoRoot, root string, tests, srcs []string, run SuiteRunner) (HeadGreen, bool) {
	if !failFirstWouldRun(repoRoot, tests, srcs) {
		return HeadGreen{}, false
	}
	out := failFirstViolatedAt(repoRoot, root, tests, srcs, run)
	if !out.Conclusive || !out.violated {
		return HeadGreen{}, false
	}
	return HeadGreen{
		Names:  goRunNames(out.runner.Args),
		Inputs: proofInputs(repoRoot, tests),
	}, true
}

// goRunNames is the test names a Go proof's `-run ^(A|B)$` filter selects —
// the tests the proof ran, and so the ones that passed when it exited 0. A
// filter of any other shape, or none, names nothing.
func goRunNames(args []string) []string {
	var filter string
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "-run="); ok {
			filter = v
			break
		}
		if a == "-run" && i+1 < len(args) {
			filter = args[i+1]
			break
		}
	}
	inner, ok := strings.CutPrefix(filter, "^(")
	if !ok {
		return nil
	}
	inner, ok = strings.CutSuffix(inner, ")$")
	if !ok || inner == "" {
		return nil
	}
	return strings.Split(inner, "|")
}

// splitAdvice is what the refusal of a commit whose tests already pass at
// HEAD adds to its message: which tests, in which files, and the commands
// that commit them alone before the rest.
func splitAdvice(tests, names []string) string {
	var b strings.Builder
	b.WriteString("\n\n")
	if len(names) > 0 {
		fmt.Fprintf(&b, "Tests that pass at HEAD: %s\n", strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "Test files: %s\n", strings.Join(tests, ", "))
	b.WriteString("They need no implementation to pass, so commit them alone first and the rest second:\n")
	b.WriteString("    aphrollo gate split-commit --dry                              (names both commits, writes nothing)\n")
	b.WriteString("    aphrollo gate split-commit -m \"<what the tests pin>\"          (commits the tests alone; the rest stays staged)\n")
	b.WriteString("    git commit                                                    (the rest, with its own message)\n")
	return b.String()
}
