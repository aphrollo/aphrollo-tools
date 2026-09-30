package suite

import (
	"fmt"
	"os"
	"slices"
)

// relatedWithinBudget is r, or the full suite of r's tool when r is a
// related-tests run (vitest `related <files>`, jest `--findRelatedTests
// <files>`) whose line passes budget. Neither tool reads its files from a
// file or stdin, and its runs are not independent per file (a batch that
// selects no test reads as an empty run), so the line is not split: the full
// suite is a superset of every related selection, so the verdict loses only
// time. This is the executor's own bound, holding whichever stage built the
// runner and whether it starts through npx or `node <bin entry>` (issue
// #1008: a merge commit's changed set is not splittable, and the stage that
// built the line is not the one that starts it).
func relatedWithinBudget(r Runner, budget int) Runner {
	line := len(cmdString(r))
	if line <= budget {
		return r
	}
	full, ok := relatedFullSuite(r)
	if !ok {
		return r
	}
	fmt.Fprintf(os.Stderr, "gate: the related-tests command is %d chars, past the %d-char command-line budget; running the full suite (%s) instead\n",
		line, budget, cmdString(full))
	return full
}

// relatedFullSuite is the full-suite runner of the tool r runs a related
// selection with, and whether r is such a run: the tool's own words before
// the related verb, then `run` for vitest.
func relatedFullSuite(r Runner) (Runner, bool) {
	full := r
	switch npmTestToolOf(r) {
	case "vitest":
		if len(r.Args) < 2 || r.Args[1] != "related" {
			return r, false
		}
		full.Args = []string{r.Args[0], "run"}
	case "jest":
		at := slices.Index(r.Args, "--findRelatedTests")
		if at < 0 {
			return r, false
		}
		full.Args = slices.Clone(r.Args[:at])
	default:
		return r, false
	}
	return full, true
}
