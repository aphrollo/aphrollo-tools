package merge

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// retroGh is the collector's one way to reach GitHub: the suite package's
// deadline-bounded gh runner, behind a var so a test replays recorded
// output instead of reaching the network.
var retroGh = func(dir string, timeout time.Duration, args ...string) (string, error) {
	return runGhTimeout(dir, timeout, args...)
}

// retroBudget bounds the whole collection, every gh call of it together: the
// merge has already landed, and a retro that held the terminal longer than
// this would be delaying work to describe it.
var retroBudget = 45 * time.Second

// retroNow is the clock the budget and the refusal window read.
var retroNow = time.Now

// retroPR is `gh pr view --json number,url,createdAt,mergedAt,headRefName`.
type retroPR struct {
	Number      int       `json:"number"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"createdAt"`
	MergedAt    time.Time `json:"mergedAt"`
	HeadRefName string    `json:"headRefName"`
}

// retroRun is one row of `gh run list --json ...`, plus the failed jobs the
// collector read for it.
type retroRun struct {
	ID         int64      `json:"databaseId"`
	HeadSha    string     `json:"headSha"`
	Conclusion string     `json:"conclusion"`
	Attempt    int        `json:"attempt"`
	CreatedAt  time.Time  `json:"createdAt"`
	Workflow   string     `json:"workflowName"`
	Failed     []retroJob `json:"-"`
}

// retroJob is one job of `gh run view --json jobs`.
type retroJob struct {
	ID         int64         `json:"databaseId"`
	Name       string        `json:"name"`
	Conclusion string        `json:"conclusion"`
	Mutants    *retroMutants `json:"-"`
}

// retroMutants is what a failed mutation job's log says it measured.
type retroMutants struct {
	Survivors, TimedOut, Unmeasured int
}

const (
	retroPRFields  = "number,url,createdAt,mergedAt,headRefName"
	retroRunFields = "databaseId,headSha,conclusion,attempt,createdAt,workflowName"
)

// retroCollector runs one merge's gh batch against one deadline.
type retroCollector struct {
	dir      string
	deadline time.Time
}

// gh runs one call with whatever is left of the budget, and refuses to start
// one once nothing is.
func (c *retroCollector) gh(args ...string) (string, error) {
	left := c.deadline.Sub(retroNow())
	if left <= 0 {
		return "", fmt.Errorf("gh budget of %s spent", retroBudget)
	}
	out, err := retroGh(c.dir, left, args...)
	if err != nil {
		return "", fmt.Errorf("gh %s %s: %w", args[0], args[1], err)
	}
	return out, nil
}

// collect reads the PR, its pull_request runs, the failed jobs of each
// failed run and the log of each failed mutation job. The first failure ends
// the batch: a retro built on half the facts would read as the whole story.
func (c *retroCollector) collect(pr int) (retroPR, []retroRun, error) {
	var info retroPR
	out, err := c.gh("pr", "view", strconv.Itoa(pr), "--json", retroPRFields)
	if err != nil {
		return info, nil, err
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return info, nil, fmt.Errorf("gh pr view: %w", err)
	}
	out, err = c.gh("run", "list", "--branch", info.HeadRefName, "--event", "pull_request",
		"--limit", "100", "--json", retroRunFields)
	if err != nil {
		return info, nil, err
	}
	var runs []retroRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return info, nil, fmt.Errorf("gh run list: %w", err)
	}
	for i := range runs {
		if !retroFailed(runs[i].Conclusion) {
			continue
		}
		jobs, err := c.failedJobs(runs[i].ID)
		if err != nil {
			return info, nil, err
		}
		runs[i].Failed = jobs
	}
	return info, runs, nil
}

// failedJobs lists run's failed jobs, reading a mutation job's log for its
// counts.
func (c *retroCollector) failedJobs(run int64) ([]retroJob, error) {
	out, err := c.gh("run", "view", strconv.FormatInt(run, 10), "--json", "jobs")
	if err != nil {
		return nil, err
	}
	var view struct {
		Jobs []retroJob `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return nil, fmt.Errorf("gh run view: %w", err)
	}
	var failed []retroJob
	for _, j := range view.Jobs {
		if !retroFailed(j.Conclusion) {
			continue
		}
		if strings.Contains(j.Name, "mutants") {
			log, err := c.gh("run", "view", "--job", strconv.FormatInt(j.ID, 10), "--log-failed")
			if err != nil {
				return nil, err
			}
			j.Mutants = parseMutantsLog(log)
		}
		failed = append(failed, j)
	}
	return failed, nil
}

// retroFailed reports whether a run or job conclusion is a red.
func retroFailed(conclusion string) bool {
	return conclusion == "failure" || conclusion == "timed_out"
}

var (
	// The gate's own summary line: "mutants: 80 tested, 65 caught, 0
	// unviable, 5 missed (0 accepted), 0 unmeasured, ...".
	mutantsSummaryRe = regexp.MustCompile(`mutants: \d+ tested, \d+ caught, \d+ unviable, (\d+) missed \((\d+) accepted\), (\d+) unmeasured`)
	// gremlins' own count: "Timed out: 0, Not viable: 0, Skipped: 8496".
	mutantsTimedOutRe = regexp.MustCompile(`Timed out: (\d+),`)
)

// parseMutantsLog reads the last summary in a mutation job's log; nil when
// the log carries none.
func parseMutantsLog(log string) *retroMutants {
	sums := mutantsSummaryRe.FindAllStringSubmatch(log, -1)
	if len(sums) == 0 {
		return nil
	}
	last := sums[len(sums)-1]
	missed, _ := strconv.Atoi(last[1])
	accepted, _ := strconv.Atoi(last[2])
	unmeasured, _ := strconv.Atoi(last[3])
	m := &retroMutants{Survivors: missed - accepted, Unmeasured: unmeasured}
	if t := mutantsTimedOutRe.FindAllStringSubmatch(log, -1); len(t) > 0 {
		m.TimedOut, _ = strconv.Atoi(t[len(t)-1][1])
	}
	return m
}
