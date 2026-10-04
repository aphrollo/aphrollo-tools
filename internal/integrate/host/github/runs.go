package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// runFields is the field list asked of `gh run view --json`.
const runFields = "attempt,conclusion,databaseId,event,headBranch,headSha,jobs,status,url,workflowName"

// listFields is the field list asked of `gh run list --json`.
const listFields = "databaseId,headSha,conclusion,attempt,createdAt,workflowName"

// jsonOf runs gh and decodes its stdout into v, folding gh's own output into
// any error so a failure names its cause.
func (g *GitHub) jsonOf(v any, args ...string) error {
	out, err := g.gh(args...)
	if err != nil {
		return fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("gh %s: decoding its output: %w", strings.Join(args, " "), err)
	}
	return nil
}

// ResolveRun turns a target into a run id.
func (g *GitHub) ResolveRun(t host.RunTarget) (int64, error) {
	if t.Run > 0 {
		return t.Run, nil
	}
	var runs []struct {
		DatabaseID int64 `json:"databaseId"`
	}
	if t.Main {
		if err := g.jsonOf(&runs, "run", "list", "--branch", "main", "--workflow", t.Workflow,
			"--limit", "1", "--json", "databaseId"); err != nil {
			return 0, err
		}
		if len(runs) == 0 {
			return 0, fmt.Errorf("no %s run on main", t.Workflow)
		}
		return runs[0].DatabaseID, nil
	}
	args := []string{"pr", "view"}
	if t.PR > 0 {
		args = append(args, strconv.Itoa(t.PR))
	}
	var pr struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := g.jsonOf(&pr, append(args, "--json", "headRefOid")...); err != nil {
		return 0, err
	}
	if err := g.jsonOf(&runs, "run", "list", "--commit", pr.HeadRefOid, "--workflow", t.Workflow,
		"--limit", "1", "--json", "databaseId"); err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, fmt.Errorf("no %s run on commit %s", t.Workflow, pr.HeadRefOid)
	}
	return runs[0].DatabaseID, nil
}

// Run reads one run with its jobs and their steps.
func (g *GitHub) Run(id int64) (*host.WorkflowRun, error) {
	var r host.WorkflowRun
	if err := g.jsonOf(&r, "run", "view", strconv.FormatInt(id, 10), "--json", runFields); err != nil {
		return nil, err
	}
	return &r, nil
}

// RunsOn lists the runs of one event on a branch, newest first.
func (g *GitHub) RunsOn(branch, event string, limit int) ([]host.WorkflowRun, error) {
	var runs []host.WorkflowRun
	if err := g.jsonOf(&runs, "run", "list", "--branch", branch, "--event", event,
		"--limit", strconv.Itoa(limit), "--json", listFields); err != nil {
		return nil, err
	}
	return runs, nil
}

// JobAnnotations are the annotation messages of one job's check run.
func (g *GitHub) JobAnnotations(job int64) ([]string, error) {
	var anns []struct {
		Message string `json:"message"`
	}
	if err := g.jsonOf(&anns, "api", fmt.Sprintf("repos/{owner}/{repo}/check-runs/%d/annotations", job)); err != nil {
		return nil, err
	}
	out := make([]string, len(anns))
	for i, a := range anns {
		out[i] = a.Message
	}
	return out, nil
}

// JobLog is the failed-step log of one job. A log GitHub no longer holds is
// host.ErrLogMissing.
func (g *GitHub) JobLog(job int64) ([]byte, error) {
	args := []string{"run", "view", "--job", strconv.FormatInt(job, 10), "--log-failed"}
	out, err := g.gh(args...)
	if err != nil {
		text := string(out)
		if strings.Contains(text, "BlobNotFound") || strings.Contains(text, "log not found") {
			return nil, host.ErrLogMissing
		}
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(text))
	}
	return out, nil
}

// RunLog is the failed-step log of a whole run, exactly as gh returns it.
func (g *GitHub) RunLog(run int64) ([]byte, error) {
	args := []string{"run", "view", strconv.FormatInt(run, 10), "--log-failed"}
	out, err := g.gh(args...)
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
