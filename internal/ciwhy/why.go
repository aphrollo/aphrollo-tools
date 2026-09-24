// Package ciwhy answers "why is this CI run red?" in a few lines: it resolves
// a pipeline run from a PR, a run id or main, then prints one line per failed
// job with the evidence under it — failing Go tests with their assertion
// lines, a mutation verdict's survivors, or an infrastructure cause stated as
// one. Every GitHub read goes through the Gh seam, so the whole flow runs on
// recorded fixtures in tests.
package ciwhy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Gh runs one gh invocation and returns its combined output. On a non-zero
// exit the output still carries gh's own error text, which the caller reads.
type Gh func(ctx context.Context, args ...string) ([]byte, error)

// Target names the run to explain. Run wins over Main, Main over PR; with
// none of them set the PR is the current branch's.
type Target struct {
	Run      int64
	PR       int
	Main     bool
	Workflow string
}

// runFields is the field list asked of `gh run view --json`.
const runFields = "attempt,conclusion,databaseId,event,headBranch,headSha,jobs,status,url,workflowName"

type run struct {
	Attempt      int    `json:"attempt"`
	Conclusion   string `json:"conclusion"`
	DatabaseID   int64  `json:"databaseId"`
	Event        string `json:"event"`
	HeadBranch   string `json:"headBranch"`
	HeadSha      string `json:"headSha"`
	Status       string `json:"status"`
	URL          string `json:"url"`
	WorkflowName string `json:"workflowName"`
	Jobs         []job  `json:"jobs"`
}

type job struct {
	DatabaseID int64  `json:"databaseId"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	Steps      []step `json:"steps"`
}

type step struct {
	Name       string `json:"name"`
	Number     int    `json:"number"`
	Conclusion string `json:"conclusion"`
}

type annotation struct {
	Message string `json:"message"`
}

// failedConclusions are the job conclusions that make a run red.
var failedConclusions = map[string]bool{
	"failure":         true,
	"cancelled":       true,
	"timed_out":       true,
	"startup_failure": true,
}

// ghJSON runs gh and decodes its stdout into v, folding gh's own output into
// any error so a failure names its cause.
func ghJSON(ctx context.Context, gh Gh, v any, args ...string) error {
	out, err := gh(ctx, args...)
	if err != nil {
		return fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("gh %s: decoding its output: %w", strings.Join(args, " "), err)
	}
	return nil
}

// resolveRun turns a Target into a run id.
func resolveRun(ctx context.Context, gh Gh, t Target) (int64, error) {
	if t.Run > 0 {
		return t.Run, nil
	}
	var runs []struct {
		DatabaseID int64 `json:"databaseId"`
	}
	if t.Main {
		if err := ghJSON(ctx, gh, &runs, "run", "list", "--branch", "main", "--workflow", t.Workflow,
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
	if err := ghJSON(ctx, gh, &pr, append(args, "--json", "headRefOid")...); err != nil {
		return 0, err
	}
	if err := ghJSON(ctx, gh, &runs, "run", "list", "--commit", pr.HeadRefOid, "--workflow", t.Workflow,
		"--limit", "1", "--json", "databaseId"); err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, fmt.Errorf("no %s run on commit %s", t.Workflow, pr.HeadRefOid)
	}
	return runs[0].DatabaseID, nil
}

// Raw prints the resolved run's failed-step log exactly as gh returns it.
func Raw(ctx context.Context, gh Gh, t Target, w io.Writer) error {
	id, err := resolveRun(ctx, gh, t)
	if err != nil {
		return err
	}
	args := []string{"run", "view", strconv.FormatInt(id, 10), "--log-failed"}
	out, err := gh(ctx, args...)
	if err != nil {
		return fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	_, err = w.Write(out)
	return err
}

// Why prints the resolved run's header and one block per failed job.
func Why(ctx context.Context, gh Gh, t Target, w io.Writer) error {
	id, err := resolveRun(ctx, gh, t)
	if err != nil {
		return err
	}
	var r run
	if err := ghJSON(ctx, gh, &r, "run", "view", strconv.FormatInt(id, 10), "--json", runFields); err != nil {
		return err
	}
	state := r.Conclusion
	if state == "" {
		state = r.Status
	}
	sha := r.HeadSha[:min(7, len(r.HeadSha))]
	fmt.Fprintf(w, "run %d %s attempt %d: %s (%s %s %s)\n%s\n",
		r.DatabaseID, r.WorkflowName, r.Attempt, state, r.Event, r.HeadBranch, sha, r.URL)
	failed := 0
	for _, j := range r.Jobs {
		if !failedConclusions[j.Conclusion] {
			continue
		}
		failed++
		if err := explainJob(ctx, gh, j, w); err != nil {
			return err
		}
	}
	if failed == 0 {
		fmt.Fprintln(w, "no failed job")
	}
	return nil
}

// explainJob prints one failed job: an infrastructure cause when there is
// one, otherwise the summary of its failed-step log.
func explainJob(ctx context.Context, gh Gh, j job, w io.Writer) error {
	switch j.Conclusion {
	case "cancelled":
		fmt.Fprintf(w, "%s: infra: cancelled\n", j.Name)
		return nil
	case "timed_out":
		fmt.Fprintf(w, "%s: infra: timed out at job level\n", j.Name)
		return nil
	}
	var anns []annotation
	if err := ghJSON(ctx, gh, &anns, "api",
		fmt.Sprintf("repos/{owner}/{repo}/check-runs/%d/annotations", j.DatabaseID)); err != nil {
		return err
	}
	for _, a := range anns {
		if cause := infraCause(a.Message); cause != "" {
			fmt.Fprintf(w, "%s: infra: %s\n  %s\n", j.Name, cause, a.Message)
			return nil
		}
	}
	args := []string{"run", "view", "--job", strconv.FormatInt(j.DatabaseID, 10), "--log-failed"}
	out, err := gh(ctx, args...)
	if err != nil {
		text := string(out)
		if strings.Contains(text, "BlobNotFound") || strings.Contains(text, "log not found") {
			fmt.Fprintf(w, "%s: infra: job log not found (BlobNotFound)\n", j.Name)
			return nil
		}
		return fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(text))
	}
	fmt.Fprintf(w, "%s: %s\n", j.Name, failedAt(j))
	summariseLog(w, parseLog(out))
	return nil
}

// infraCause names the infrastructure failure an annotation reports, or ""
// when it reports none. The phrases are GitHub's own wording.
func infraCause(message string) string {
	switch {
	case strings.Contains(message, "lost communication with the server"):
		return "runner lost communication"
	case strings.Contains(message, "exceeded the maximum execution time"):
		return "timed out at job level"
	}
	return ""
}

// failedAt describes where a job failed: its first failed step, or its bare
// conclusion when no step carries one.
func failedAt(j job) string {
	for _, s := range j.Steps {
		if s.Conclusion == "failure" {
			return fmt.Sprintf("%s at step %d %q", j.Conclusion, s.Number, s.Name)
		}
	}
	return j.Conclusion
}
