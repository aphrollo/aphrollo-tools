package github

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"slices"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// ChecksAt reads every check run and commit status on ONE commit, by SHA, so a
// result can never belong to a different head than the one asked about. Each
// record is one JSON object per line, so a name with spaces stays whole.
func (g *GitHub) ChecksAt(sha string) ([]host.Check, error) {
	runs, err := g.jsonLines("api", "--paginate", "repos/{owner}/{repo}/commits/"+sha+"/check-runs",
		"--jq", `.check_runs[] | {id, name, head_sha, status, conclusion, html_url, started_at, app: .app.slug}`)
	if err != nil {
		return nil, err
	}
	runs = g.markNotStarted(g.withoutSupersededCancelled(newestPerName(runs)))
	statuses, err := g.jsonLines("api", "repos/{owner}/{repo}/commits/"+sha+"/status",
		"--jq", `.sha as $s | .statuses[] | {name: .context, head_sha: $s, `+
			`status: (if .state == "pending" then "in_progress" else "completed" end), `+
			`conclusion: .state, html_url: .target_url}`)
	if err != nil {
		return nil, err
	}
	return append(runs, statuses...), nil
}

// newestPerName keeps, of the check runs that share an app and a name, only the
// newest (the highest id): a head carries one run of a job per workflow run, and a
// run made while the PR was a draft (every job skipped) or an attempt since
// re-run must not decide what the latest one answers. Legacy commit statuses
// (no id) are left as they are. Order is kept.
func newestPerName(runs []host.Check) []host.Check {
	newest := map[string]int64{}
	for _, r := range runs {
		if r.ID == 0 {
			continue
		}
		if k := r.App + "|" + r.Name; r.ID > newest[k] {
			newest[k] = r.ID
		}
	}
	out := runs[:0:0]
	for _, r := range runs {
		if r.ID != 0 && r.ID != newest[r.App+"|"+r.Name] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// concludedFailed reports a check that completed with a conclusion that is not
// a pass: anything but success, neutral or skipped.
func concludedFailed(c host.Check) bool {
	if !strings.EqualFold(c.Status, "completed") {
		return false
	}
	switch strings.ToUpper(c.Conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED", "":
		return false
	}
	return true
}

// markNotStarted flags each failed Actions check run whose job ran no step.
// Only those are asked about; a job whose steps cannot be read stays the
// failure GitHub reported, so an unreadable answer never softens a red.
func (g *GitHub) markNotStarted(runs []host.Check) []host.Check {
	for i, r := range runs {
		if r.App != "github-actions" || r.ID == 0 || !concludedFailed(r) {
			continue
		}
		if n, err := g.jobSteps(r.ID); err == nil && n == 0 {
			runs[i].NotStarted = true
			runs[i].NotAcquired = strings.EqualFold(r.Conclusion, "cancelled") && g.hostedNotAcquired(r.ID)
		}
	}
	return runs
}

// jobSteps reads how many steps an Actions job has.
func (g *GitHub) jobSteps(id int64) (int, error) {
	out, err := g.gh("api", "repos/{owner}/{repo}/actions/jobs/"+strconv.FormatInt(id, 10), "--jq", ".steps | length")
	if err != nil {
		return 0, fmt.Errorf("gh api actions/jobs/%d: %v: %s", id, err, strings.TrimSpace(string(out)))
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func (g *GitHub) jsonLines(args ...string) ([]host.Check, error) {
	out, err := g.gh(args...)
	if err != nil {
		return nil, fmt.Errorf("gh %s: %v: %s", strings.Join(args[:2], " "), err, strings.TrimSpace(string(out)))
	}
	var rows []host.Check
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r host.Check
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("parsing gh %s: %w", strings.Join(args[:2], " "), err)
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

// RunInfo reads the workflow file and attempt of one Actions run.
func (g *GitHub) RunInfo(id int64) (host.RunInfo, error) {
	out, err := g.gh("api", "repos/{owner}/{repo}/actions/runs/"+strconv.FormatInt(id, 10),
		"--jq", `.path + " " + (.run_attempt | tostring)`)
	if err != nil {
		return host.RunInfo{}, err
	}
	path, attempt, ok := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !ok {
		return host.RunInfo{}, fmt.Errorf("unreadable run: %q", out)
	}
	path, _, _ = strings.Cut(path, "@")
	n, err := strconv.Atoi(attempt)
	if err != nil {
		return host.RunInfo{}, err
	}
	return host.RunInfo{Workflow: pathpkg.Base(path), Attempt: n}, nil
}

// PRRuns lists every pull_request run that belongs to PR number pr, whatever
// the branch's other history holds: the branch's runs are read page by page and
// kept by the pull request each one names, so a busy PR's oldest run is not cut
// off by a page limit and a reused branch name brings in no older PR's runs.
func (g *GitHub) PRRuns(branch string, pr int) ([]host.PRRun, error) {
	jq := fmt.Sprintf(`.workflow_runs[] | select(any(.pull_requests[]?; .number == %d)) | `+
		`{databaseId: .id, headSha: .head_sha, status: .status, conclusion: .conclusion, createdAt: .created_at, attempt: .run_attempt}`, pr)
	out, err := g.gh("api", "--paginate",
		"repos/{owner}/{repo}/actions/runs?event=pull_request&per_page=100&branch="+branch, "--jq", jq)
	if err != nil {
		return nil, fmt.Errorf("gh api actions/runs: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var runs []host.PRRun
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var r host.PRRun
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("gh api actions/runs: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// RunFailedJobs names the jobs of a run's attempt that failed.
func (g *GitHub) RunFailedJobs(id int64, attempt int) ([]string, error) {
	out, err := g.gh("run", "view", fmt.Sprint(id), "--attempt", fmt.Sprint(attempt), "--json", "jobs",
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

// RunFirstAttempt reads how the first attempt of a run ended.
func (g *GitHub) RunFirstAttempt(id int64) (status, conclusion string, err error) {
	out, err := g.gh("api", fmt.Sprintf("repos/{owner}/{repo}/actions/runs/%d/attempts/1", id),
		"--jq", `[.status, .conclusion // ""] | join(" ")`)
	if err != nil {
		return "", "", fmt.Errorf("gh api run %d attempt 1: %v: %s", id, err, strings.TrimSpace(string(out)))
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", "", fmt.Errorf("gh api run %d attempt 1: no status", id)
	}
	status = f[0]
	if len(f) > 1 {
		conclusion = f[1]
	}
	return status, conclusion, nil
}

// notAcquiredMessage is the annotation GitHub puts on a hosted job no runner
// picked up in time.
const notAcquiredMessage = "was not acquired by Runner of type hosted"

// hostedNotAcquired reports whether the job's annotations say no hosted runner
// ever acquired it. An unreadable annotation list says no.
func (g *GitHub) hostedNotAcquired(id int64) bool {
	anns, err := g.JobAnnotations(id)
	if err != nil {
		return false
	}
	for _, a := range anns {
		if strings.Contains(a, notAcquiredMessage) {
			return true
		}
	}
	return false
}

// RerunFailedJobs asks GitHub to run again the failed jobs of one Actions run.
func (g *GitHub) RerunFailedJobs(run int64) error {
	out, err := g.gh("api", "--method", "POST", "repos/{owner}/{repo}/actions/runs/"+strconv.FormatInt(run, 10)+"/rerun-failed-jobs")
	if err != nil {
		return fmt.Errorf("gh api actions/runs/%d/rerun-failed-jobs: %v: %s", run, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// withoutSupersededCancelled drops each cancelled Actions job of a run that a
// later run of the same workflow replaced on this commit. A concurrency group
// cancels the earlier run when a later one starts, and a job the later run never
// made has no newer check of its name to hide the cancelled one: it would read
// as a failure of a commit that was tested whole by the run that replaced it.
// A run whose workflow cannot be read replaces nothing, so an unreadable answer
// never softens a check.
func (g *GitHub) withoutSupersededCancelled(runs []host.Check) []host.Check {
	cancelled := map[int64]bool{}
	var all []int64
	for _, c := range runs {
		id := host.RunID(c)
		if c.App != "github-actions" || id == 0 {
			continue
		}
		if !slices.Contains(all, id) {
			all = append(all, id)
		}
		if strings.EqualFold(c.Status, "completed") && strings.EqualFold(c.Conclusion, "cancelled") {
			cancelled[id] = true
		}
	}
	if len(cancelled) == 0 || len(all) < 2 {
		return runs
	}
	workflow := map[int64]string{}
	for _, id := range all {
		if info, err := g.RunInfo(id); err == nil {
			workflow[id] = info.Workflow
		}
	}
	superseded := map[int64]bool{}
	for id := range cancelled {
		for _, later := range all {
			if later > id && workflow[id] != "" && workflow[later] == workflow[id] {
				superseded[id] = true
			}
		}
	}
	if len(superseded) == 0 {
		return runs
	}
	out := runs[:0:0]
	for _, c := range runs {
		if superseded[host.RunID(c)] && strings.EqualFold(c.Status, "completed") && strings.EqualFold(c.Conclusion, "cancelled") {
			continue
		}
		out = append(out, c)
	}
	return out
}
