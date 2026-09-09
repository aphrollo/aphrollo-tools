package workspace

import (
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// premergeGate is the seam over the local pre-merge gate the merge verb runs
// before it asks GitHub to land the lane. A package var so merge tests drive
// the verb's own logic without a checkout, a suite or a mutation run,
// mirroring the gh seams next to it.
//
// It calls the gate rather than re-checking anything here: tdd.GatePRMerge
// builds the merge locally and hands the tree to the SAME Mechanical stage
// the pre-merge-commit hook runs, so the PR path and the local `git merge`
// path are judged by one gate and not two lookalikes. A repo that declares no
// mutants-at-merge is not gated here and pays nothing.
var premergeGate = func(t *Target, log io.Writer) error {
	return tdd.GatePRMerge(t.Worktree, tdd.RunSuite(tdd.DefaultPrecommitTimeout), log)
}
