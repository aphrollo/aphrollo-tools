package workspace

import (
	"io"
	"strings"
	"time"

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
//
// verdict is what GitHub's checks said about the PR head, or nil when local
// CI stood in for them. When the tree the gate would test is the tree those
// checks tested, the gate takes their word for the suites (see
// tdd.GatePRMergeReusingCI) instead of running them again on this box.
var premergeGate = func(t *Target, verdict *tdd.CIVerdict, log io.Writer) error {
	run := tdd.RunSuite(tdd.DefaultPrecommitTimeout)
	if verdict == nil {
		return tdd.GatePRMerge(t.Worktree, run, log)
	}
	return tdd.GatePRMergeReusingCI(t.Worktree, run, log, *verdict)
}

// ghVerdictChecks reads the check runs of one commit for the gate to take in
// place of its own suites. A read that fails is no checks, which the gate
// answers by running everything locally: never a pass. A package var so tests
// state what GitHub answered without a network.
var ghVerdictChecks = func(wt, sha string) []CheckRun {
	runs, err := ghChecksAt(wt, sha)
	if err != nil {
		return nil
	}
	return runs
}

// ciVerdictOf is GitHub's answer for a PR in the form the gate reads: each
// check on the head, passed only when it concluded `success` (a skipped or
// neutral check ran nothing worth taking in place of a suite).
func ciVerdictOf(wt string, pr int, ci CIStatus) *tdd.CIVerdict {
	v := &tdd.CIVerdict{PR: pr, HeadSHA: ci.SHA}
	for _, c := range ghVerdictChecks(wt, ci.SHA) {
		if c.SHA != ci.SHA {
			continue
		}
		started, _ := time.Parse(time.RFC3339, c.StartedAt)
		v.Checks = append(v.Checks, tdd.CIVerdictCheck{
			Name:    c.Name,
			Passed:  strings.EqualFold(c.Status, "completed") && strings.EqualFold(c.Conclusion, "success"),
			Started: started,
		})
	}
	return v
}
