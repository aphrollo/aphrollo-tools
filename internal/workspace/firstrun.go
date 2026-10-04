package workspace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// prRun is one workflow run of a pull_request event, as gh lists it.
type prRun struct {
	ID         int64  `json:"databaseId"`
	SHA        string `json:"headSha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	CreatedAt  string `json:"createdAt"`
}

// ghPRRuns lists the pull_request runs of a branch.
var ghPRRuns = func(wt, branch string) ([]prRun, error) {
	out, err := ghCombinedOutput(wt, "run", "list", "--branch", branch, "--event", "pull_request",
		"--limit", "100", "--json", "databaseId,headSha,status,conclusion,createdAt")
	if err != nil {
		return nil, fmt.Errorf("gh run list: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var runs []prRun
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("gh run list: %w", err)
	}
	return runs, nil
}

// ghRunFailedJobs names the jobs of a run that failed.
var ghRunFailedJobs = func(wt string, id int64) ([]string, error) {
	out, err := ghCombinedOutput(wt, "run", "view", fmt.Sprint(id), "--json", "jobs",
		"--jq", `[.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out" or .conclusion == "startup_failure") | .name] | join("\n")`)
	if err != nil {
		return nil, fmt.Errorf("gh run view %d: %v: %s", id, err, strings.TrimSpace(string(out)))
	}
	var names []string
	for line := range strings.Lines(string(out)) {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// recordFirstRunCI writes the settled result of the PR's FIRST head, at the time
// that head's runs started. CI's first answer is red more often than the verbs
// see: a push reads it while it is still pending, and a session that reads the
// red with gh and pushes a fix shows `workspace merge` only the green of the
// later head. Reading the run list when the merge looks closes that: the first
// run's conclusion is recorded wherever it was read, once per commit (a result
// an earlier verb recorded is kept), and its own time puts it ahead of the
// later head's result. A first run still going, or cancelled (a newer push
// replaced it), settled nothing and is not recorded. Best effort.
func recordFirstRunCI(wt, branch string, pr int) {
	runs, err := ghPRRuns(wt, branch)
	if err != nil || len(runs) == 0 {
		return
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].CreatedAt < runs[j].CreatedAt })
	first := runs[0].SHA
	state, failedRun := "green", int64(0)
	for _, r := range runs {
		if r.SHA != first {
			continue
		}
		switch {
		case r.Status != "completed":
			return
		case r.Conclusion == "failure" || r.Conclusion == "timed_out" || r.Conclusion == "startup_failure":
			state, failedRun = "red", r.ID
		case r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral":
			return
		}
	}
	detail := map[string]string{"sha": first, "ci": tdd.CIGithub, "pr": fmt.Sprint(pr)}
	if state == "red" {
		names, err := ghRunFailedJobs(wt, failedRun)
		if err != nil {
			return
		}
		detail["cause"] = ciCause(names)
	}
	tdd.AppendEventOnce(tdd.Event{Kind: "ci", Root: wt, Verdict: state, At: runs[0].CreatedAt, Detail: detail}, "sha")
}
