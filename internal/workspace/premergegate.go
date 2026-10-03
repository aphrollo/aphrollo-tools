package workspace

import (
	"fmt"
	"io"
	pathpkg "path"
	"regexp"
	"strconv"
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

// runInfo is what the Actions run behind a check says about itself.
type runInfo struct {
	Workflow string // the workflow file's base name, such as pipeline.yml
	Attempt  int
}

// ghRunInfo reads the workflow file and attempt of one Actions run. A package
// var so tests state what Actions answered without a network.
var ghRunInfo = func(wt string, id int64) (runInfo, error) {
	out, err := ghCombinedOutput(wt, "api", "repos/{owner}/{repo}/actions/runs/"+strconv.FormatInt(id, 10),
		"--jq", `.path + " " + (.run_attempt | tostring)`)
	if err != nil {
		return runInfo{}, err
	}
	path, attempt, ok := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !ok {
		return runInfo{}, fmt.Errorf("unreadable run: %q", out)
	}
	path, _, _ = strings.Cut(path, "@")
	n, err := strconv.Atoi(attempt)
	if err != nil {
		return runInfo{}, err
	}
	return runInfo{Workflow: pathpkg.Base(path), Attempt: n}, nil
}

var actionsRunRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// ciVerdictOf is GitHub's answer for a PR in the form the gate reads: each
// check on the head, passed only when it concluded `success` (a skipped or
// neutral check ran nothing worth taking in place of a suite), with the app,
// workflow file and attempt of the Actions run behind it. A run that cannot be
// read leaves its workflow and attempt empty, which the gate does not count.
func ciVerdictOf(wt string, pr int, ci CIStatus) *tdd.CIVerdict {
	v := &tdd.CIVerdict{PR: pr, HeadSHA: ci.SHA}
	runs := map[int64]runInfo{}
	for _, c := range ghVerdictChecks(wt, ci.SHA) {
		if c.SHA != ci.SHA {
			continue
		}
		started, _ := time.Parse(time.RFC3339, c.StartedAt)
		check := tdd.CIVerdictCheck{
			Name:    c.Name,
			Passed:  strings.EqualFold(c.Status, "completed") && strings.EqualFold(c.Conclusion, "success"),
			Started: started,
			App:     c.App,
		}
		if m := actionsRunRe.FindStringSubmatch(c.URL); m != nil && c.App == "github-actions" {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			info, seen := runs[id]
			if !seen {
				info, _ = ghRunInfo(wt, id)
				runs[id] = info
			}
			check.Workflow, check.Attempt = info.Workflow, info.Attempt
		}
		v.Checks = append(v.Checks, check)
	}
	return v
}
