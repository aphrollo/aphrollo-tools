// Package ciwhy answers "why is this CI run red?" in a few lines: it resolves
// a pipeline run from a PR, a run id or main, then prints one line per failed
// job with the evidence under it — failing Go tests with their assertion
// lines, a mutation verdict's survivors, or an infrastructure cause stated as
// one. Every read of the code host goes through the host port, so the whole
// flow runs on recorded responses in tests.
package ciwhy

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// Target names the run to explain. Run wins over Main, Main over PR; with
// none of them set the PR is the current branch's.
type Target = host.RunTarget

// failedConclusions are the job conclusions that make a run red.
var failedConclusions = map[string]bool{
	"failure":         true,
	"cancelled":       true,
	"timed_out":       true,
	"startup_failure": true,
}

// Raw prints the resolved run's failed-step log exactly as the host returns it.
func Raw(h host.Runs, t Target, w io.Writer) error {
	id, err := h.ResolveRun(t)
	if err != nil {
		return err
	}
	out, err := h.RunLog(id)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// Why prints the resolved run's header and one block per failed job.
func Why(h host.Runs, t Target, w io.Writer) error {
	id, err := h.ResolveRun(t)
	if err != nil {
		return err
	}
	r, err := h.Run(id)
	if err != nil {
		return err
	}
	state := r.Conclusion
	if state == "" {
		state = r.Status
	}
	sha := r.HeadSHA[:min(7, len(r.HeadSHA))]
	fmt.Fprintf(w, "run %d %s attempt %d: %s (%s %s %s)\n%s\n",
		r.ID, r.Workflow, r.Attempt, state, r.Event, r.HeadBranch, sha, r.URL)
	failed := 0
	for _, j := range r.Jobs {
		if !failedConclusions[j.Conclusion] {
			continue
		}
		failed++
		if err := explainJob(h, j, w); err != nil {
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
func explainJob(h host.Runs, j host.Job, w io.Writer) error {
	switch j.Conclusion {
	case "cancelled":
		fmt.Fprintf(w, "%s: infra: cancelled\n", j.Name)
		return nil
	case "timed_out":
		fmt.Fprintf(w, "%s: infra: timed out at job level\n", j.Name)
		return nil
	}
	anns, err := h.JobAnnotations(j.ID)
	if err != nil {
		return err
	}
	for _, a := range anns {
		if cause := infraCause(a); cause != "" {
			fmt.Fprintf(w, "%s: infra: %s\n  %s\n", j.Name, cause, a)
			return nil
		}
	}
	out, err := h.JobLog(j.ID)
	if err != nil {
		if errors.Is(err, host.ErrLogMissing) {
			fmt.Fprintf(w, "%s: infra: job log not found (BlobNotFound)\n", j.Name)
			return nil
		}
		return err
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
func failedAt(j host.Job) string {
	for _, s := range j.Steps {
		if s.Conclusion == "failure" {
			return fmt.Sprintf("%s at step %d %q", j.Conclusion, s.Number, s.Name)
		}
	}
	return j.Conclusion
}
