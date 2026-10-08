package suite

import (
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// argvBudgetFn is the longest command line the executor starts for a command,
// a seam so a test can hold it to Windows's figure on any platform.
var argvBudgetFn = argvbatch.BudgetFor

// runBatched runs r through one, as the several commands its line splits
// into when it passes budget (argvbatch.SplitCommand): cargo's `-p` list, a
// `go test` or `golangci-lint run` package list. A line within budget, or of
// a shape with no list, is one run of r itself.
//
// The runs go in order and share one deadline, the earlier of the runner's
// own and limit from now, so a long list never takes a timeout per batch.
// The first run that does not pass, or times out, ends the run and is the
// verdict: the outputs before it and its own come back joined, durations
// summed.
func runBatched(r Runner, root string, limit time.Duration, budget int, one SuiteRunner) SuiteResult {
	r = relatedWithinBudget(r, budget)
	batches := argvbatch.SplitCommand(r.Cmd, r.Args, budget)
	if len(batches) < 2 {
		return one(r, root)
	}
	deadline := time.Now().Add(limit)
	if !r.Deadline.IsZero() && r.Deadline.Before(deadline) {
		deadline = r.Deadline
	}
	var merged SuiteResult
	for _, args := range batches {
		b := r
		b.Args = args
		b.Deadline = deadline
		res := one(b, root)
		joinResult(&merged, res)
		if !res.Passed || res.TimedOut {
			return merged
		}
	}
	return merged
}

// joinResult folds the result of the next run into merged: its output after
// the earlier ones', durations summed, and the verdict fields the latest
// run's own.
func joinResult(merged *SuiteResult, res SuiteResult) {
	merged.Output += res.Output
	merged.GoTestJSON += res.GoTestJSON
	merged.Duration += res.Duration
	merged.Dir = res.Dir
	merged.Passed = res.Passed
	merged.TimedOut = res.TimedOut
	merged.Inconclusive = res.Inconclusive
	if merged.Err == "" {
		merged.Err = res.Err
	}
}
