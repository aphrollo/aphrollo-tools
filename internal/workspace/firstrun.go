package workspace

import (
	"fmt"
	"sort"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ghPRRuns lists every pull_request run that belongs to PR number pr, whatever
// the branch's other history holds.
var ghPRRuns = func(wt, branch string, pr int) ([]prRun, error) {
	return hostFor(wt).PRRuns(branch, pr)
}

// ghRunFailedJobs names the jobs of a run that failed.
var ghRunFailedJobs = func(wt string, id int64, attempt int) ([]string, error) {
	return hostFor(wt).RunFailedJobs(id, attempt)
}

// failedConclusion is a run conclusion that makes a head red.
func failedConclusion(c string) bool {
	return c == "failure" || c == "timed_out" || c == "startup_failure"
}

// recordFirstRunCI writes the settled result of the PR's FIRST head, at the time
// that head's runs started. CI's first answer is red more often than the verbs
// see: a push reads CI while it is still pending, and a session that reads the
// red with gh and pushes a fix shows `workspace merge` only the green of the
// later head. Reading the run list when the merge looks closes that: the first
// run's conclusion is recorded wherever it was read, once per commit (a result
// an earlier verb recorded is kept), and its own time puts it ahead of the
// later head's result. The first head is the one of the PR's own earliest run.
// Any failed run on it is red, whatever else is still going; with no failure it
// is settled only when every run finished and none was cancelled (a newer push
// replaced it). Best effort.
func recordFirstRunCI(wt, branch string, pr int) {
	runs, err := ghPRRuns(wt, branch, pr)
	if err != nil || len(runs) == 0 {
		return
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].CreatedAt < runs[j].CreatedAt })
	first := runs[0].SHA
	red, open, failedRun := false, false, int64(0)
	for _, r := range runs {
		if r.SHA != first {
			continue
		}
		// The listing shows a run's latest attempt: a failure fixed by a rerun reads
		// green there, and "first run" means the first attempt.
		if r.Attempt > 1 {
			status, conclusion, err := ghRunFirstAttempt(wt, r.ID)
			if err != nil {
				return
			}
			r.Status, r.Conclusion = status, conclusion
		}
		switch {
		case r.Status == "completed" && failedConclusion(r.Conclusion):
			if !red {
				failedRun = r.ID
			}
			red = true
		case r.Status != "completed", r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral":
			open = true
		}
	}
	if open && !red {
		return
	}
	state := "green"
	detail := map[string]string{"sha": first, "ci": tdd.CIGithub, "pr": fmt.Sprint(pr)}
	if red {
		state = "red"
		names, err := ghRunFailedJobs(wt, failedRun, 1)
		if err != nil {
			return
		}
		detail["cause"] = ciCause(names)
	}
	tdd.AppendEventOnce(tdd.Event{Kind: "ci", Root: wt, Verdict: state, At: runs[0].CreatedAt, Detail: detail}, "sha")
}

// ghRunFirstAttempt reads how the first attempt of a run ended.
var ghRunFirstAttempt = func(wt string, id int64) (status, conclusion string, err error) {
	return hostFor(wt).RunFirstAttempt(id)
}
