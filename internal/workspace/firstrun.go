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

// ghPRRuns lists every pull_request run that belongs to PR number pr, whatever
// the branch's other history holds: the branch's runs are read page by page and
// kept by the pull request each one names, so a busy PR's oldest run is not
// cut off by a page limit and a reused branch name brings in no older PR's runs.
var ghPRRuns = func(wt, branch string, pr int) ([]prRun, error) {
	jq := fmt.Sprintf(`.workflow_runs[] | select(any(.pull_requests[]?; .number == %d)) | `+
		`{databaseId: .id, headSha: .head_sha, status: .status, conclusion: .conclusion, createdAt: .created_at}`, pr)
	out, err := ghCombinedOutput(wt, "api", "--paginate",
		"repos/{owner}/{repo}/actions/runs?event=pull_request&per_page=100&branch="+branch, "--jq", jq)
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var runs []prRun
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var r prRun
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("gh api actions/runs: %w", err)
		}
		runs = append(runs, r)
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
		names, err := ghRunFailedJobs(wt, failedRun)
		if err != nil {
			return
		}
		detail["cause"] = ciCause(names)
	}
	tdd.AppendEventOnce(tdd.Event{Kind: "ci", Root: wt, Verdict: state, At: runs[0].CreatedAt, Detail: detail}, "sha")
}
