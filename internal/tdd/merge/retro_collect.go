package merge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
)

// retroRunner is gh itself as the collector's host runs it: nil is the gh on
// PATH, and a test hands in one that replays recorded output instead of
// reaching the network.
var retroRunner github.Runner

// retroBudget bounds the whole collection, every host call of it together: the
// merge has already landed, and a retro that held the terminal longer than
// this would be delaying work to describe it.
var retroBudget = 45 * time.Second

// retroNow is the clock the budget and the refusal window read.
var retroNow = time.Now

// retroPR is the merged PR as the retro reads it.
type retroPR struct {
	Number      int
	URL         string
	CreatedAt   time.Time
	MergedAt    time.Time
	HeadRefName string
}

// retroRun is one pull_request run of the PR's branch, plus the failed jobs
// the collector read for it.
type retroRun struct {
	ID         int64
	HeadSha    string
	Conclusion string
	Attempt    int
	CreatedAt  time.Time
	Workflow   string
	Failed     []retroJob
}

// retroJob is one failed job of a run.
type retroJob struct {
	ID         int64
	Name       string
	Conclusion string
	Mutants    *retroMutants
}

// retroMutants is what a failed mutation job's log says it measured.
type retroMutants struct {
	Survivors, TimedOut, Unmeasured int
}

// retroCollector runs one merge's host batch against one deadline.
type retroCollector struct {
	dir      string
	deadline time.Time
}

// host is the code host bounded by whatever is left of the budget; it refuses
// once nothing is.
func (c *retroCollector) host() (host.Host, error) {
	left := c.deadline.Sub(retroNow())
	if left <= 0 {
		return nil, fmt.Errorf("gh budget of %s spent", retroBudget)
	}
	return github.New(github.Options{Dir: c.dir, Timeout: left, Runner: retroRunner}), nil
}

// collect reads the PR, its pull_request runs, the failed jobs of each
// failed run and the log of each failed mutation job. The first failure ends
// the batch: a retro built on half the facts would read as the whole story.
func (c *retroCollector) collect(pr int) (retroPR, []retroRun, error) {
	var info retroPR
	h, err := c.host()
	if err != nil {
		return info, nil, err
	}
	sum, err := h.Summary(pr)
	if err != nil {
		return info, nil, err
	}
	info = retroPR{Number: sum.Number, URL: sum.URL, CreatedAt: sum.CreatedAt, MergedAt: sum.MergedAt, HeadRefName: sum.HeadRef}
	if h, err = c.host(); err != nil {
		return info, nil, err
	}
	listed, err := h.RunsOn(info.HeadRefName, "pull_request", 100)
	if err != nil {
		return info, nil, err
	}
	runs := make([]retroRun, len(listed))
	for i, r := range listed {
		runs[i] = retroRun{ID: r.ID, HeadSha: r.HeadSHA, Conclusion: r.Conclusion, Attempt: r.Attempt, CreatedAt: r.CreatedAt, Workflow: r.Workflow}
		if !retroFailed(r.Conclusion) {
			continue
		}
		jobs, err := c.failedJobs(r.ID)
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
	h, err := c.host()
	if err != nil {
		return nil, err
	}
	view, err := h.Run(run)
	if err != nil {
		return nil, err
	}
	var failed []retroJob
	for _, j := range view.Jobs {
		if !retroFailed(j.Conclusion) {
			continue
		}
		job := retroJob{ID: j.ID, Name: j.Name, Conclusion: j.Conclusion}
		if strings.Contains(j.Name, "mutants") {
			if h, err = c.host(); err != nil {
				return nil, err
			}
			log, err := h.JobLog(j.ID)
			if err != nil {
				return nil, err
			}
			job.Mutants = parseMutantsLog(string(log))
		}
		failed = append(failed, job)
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
