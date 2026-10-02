package ghworkflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"
)

// Jobs run one at a time, in needs order, unless Options.Jobs allows more: local
// CI shares its box with whoever is working on it, and every job at once is
// what turned a five minute build into a thirty minute timeout (#1103). With a
// limit above one, independent jobs run together, each starting when the jobs
// it needs have finished; their output is prefixed with the job's name, a whole
// line at a time, and the summary keeps the order a serial run would give.

// jobTimeoutUnit is what a job's timeout-minutes counts in. A variable so a
// test does not wait a minute.
var jobTimeoutUnit = time.Minute

// stateTable is where a finished job leaves its result for the jobs that need it.
type stateTable struct {
	mu   sync.Mutex
	byID map[string]*jobState
}

func (t *stateTable) get(id string) *jobState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.byID[id]
}

func (t *stateTable) set(id string, st *jobState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byID[id] = st
}

// describe prints how many jobs run at once and how long a step may take.
func (o Options) describe(out io.Writer) {
	fmt.Fprintf(out, "ci run: priority: %s\n", priorityNote())
	fmt.Fprintf(out, "ci run: jobs: %s; each step stops after %v (raise either with --ci-jobs and --ci-timeout, or ci-jobs and ci-timeout in aphrollo.toml)\n",
		jobsPhrase(o.Jobs), o.StepTimeout)
}

func jobsPhrase(n int) string {
	if n <= 1 {
		return "one at a time, in needs order"
	}
	return fmt.Sprintf("up to %d at once, each after the jobs it needs", n)
}

// runJobs runs a workflow's jobs, already in needs order, and returns their
// results in that order. With a limit above one, a job starts as soon as a
// slot is free and the jobs it needs have finished, the earliest in needs order
// first, so which jobs run together does not depend on timing.
func runJobs(ctx context.Context, wf *Workflow, jobs []*Job, opt Options, tmp string) []JobResult {
	states := &stateTable{byID: map[string]*jobState{}}
	results := make([]JobResult, len(jobs))
	if opt.Jobs <= 1 {
		for i, j := range jobs {
			results[i] = runJob(ctx, wf, j, opt, tmp, states)
		}
		return results
	}
	mux := &lineMux{w: opt.Out}
	started := make([]bool, len(jobs))
	done := make([]bool, len(jobs))
	finished := map[string]bool{}
	ended := make(chan int)
	for range jobs {
		for i, j := range jobs {
			if inFlight(started, done) >= opt.Jobs {
				break
			}
			if started[i] || !allDone(j.Needs, finished) {
				continue
			}
			started[i] = true
			go func() {
				out := &jobWriter{mux: mux, prefix: "[" + j.ID + "] "}
				jobOpt := opt
				jobOpt.Out = out
				results[i] = runJob(ctx, wf, j, jobOpt, tmp, states)
				out.flush()
				ended <- i
			}()
		}
		if inFlight(started, done) == 0 {
			break
		}
		i := <-ended
		done[i] = true
		finished[jobs[i].ID] = true
	}
	return results
}

// inFlight is how many jobs have started and not yet ended.
func inFlight(started, done []bool) int {
	n := 0
	for i, s := range started {
		if s && !done[i] {
			n += 1
		}
	}
	return n
}

// runJob runs one job, records what the jobs that need it will read, and says
// how it ended.
func runJob(ctx context.Context, wf *Workflow, j *Job, opt Options, tmp string, states *stateTable) JobResult {
	r := &jobRun{wf: wf, job: j, opt: opt, tmp: filepath.Join(tmp, j.ID), states: states}
	res := r.run(ctx)
	states.set(j.ID, &jobState{result: res.Result, outputs: r.outputs})
	res.Workflow = wf.File
	label := j.ID
	if res.Detail != "" {
		label += " (" + res.Detail + ")"
	}
	fmt.Fprintf(opt.Out, "ci run: job %s: %s\n", label, res.Result)
	return res
}

// lineMux lets several jobs share one writer without their lines running
// together: each line goes out whole.
type lineMux struct {
	mu sync.Mutex
	w  io.Writer
}

func (m *lineMux) writeLine(prefix string, line []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _ = m.w.Write(append([]byte(prefix), line...))
}

// jobWriter is one job's output: held until a line is whole, then written with
// the job's name in front.
type jobWriter struct {
	mux    *lineMux
	prefix string
	mu     sync.Mutex
	buf    []byte
}

func (w *jobWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		end := bytes.IndexByte(w.buf, '\n')
		if end < 0 {
			return len(p), nil
		}
		w.mux.writeLine(w.prefix, w.buf[:end+1])
		w.buf = w.buf[end+1:]
	}
}

// flush writes what a job printed after its last newline, ended with one.
func (w *jobWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.mux.writeLine(w.prefix, append(w.buf, '\n'))
		w.buf = nil
	}
}

// stepTimeoutError is a step stopped at its own time limit.
type stepTimeoutError struct{ limit time.Duration }

func (e *stepTimeoutError) Error() string {
	return fmt.Sprintf("stopped after %v (the step timeout; raise it with --ci-timeout or ci-timeout in aphrollo.toml)", e.limit)
}

// failureDetail is how a failed step is named in its job's result: the step,
// and the limit when it was its own time limit that stopped it.
func (r *jobRun) failureDetail(st *Step) string {
	if r.timedOut > 0 {
		return fmt.Sprintf("step timed out after %v: %s", r.timedOut, st.Label())
	}
	return "step failed: " + st.Label()
}

// stopReason is why a step that was running when its context ended did not
// finish: its own time limit, the job's timeout-minutes, the run's deadline or
// a cancel. A step that failed on its own keeps its own error.
func (r *jobRun) stopReason(ctx, stepCtx context.Context, err error) error {
	switch {
	case stepCtx.Err() == nil:
		return err
	case ctx.Err() == nil:
		return &stepTimeoutError{limit: r.opt.StepTimeout}
	case errors.Is(ctx.Err(), context.DeadlineExceeded) && r.job.TimeoutMin > 0:
		return fmt.Errorf("stopped when the job's timeout-minutes (%d) ran out", r.job.TimeoutMin)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return errors.New("stopped when the run's deadline passed")
	}
	return errors.New("stopped: the run was cancelled")
}

// lineEnder passes output through and remembers whether it stopped mid-line, so
// a step that ends without a newline does not have the next line of the run
// printed on its last one.
type lineEnder struct {
	mu      sync.Mutex
	w       io.Writer
	partial bool
}

func (l *lineEnder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(p) > 0 {
		l.partial = p[len(p)-1] != '\n'
	}
	return l.w.Write(p)
}

// flush ends a line that was left open.
func (l *lineEnder) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.partial {
		_, _ = l.w.Write([]byte("\n"))
		l.partial = false
	}
}

// Refused lists every step the run did not run because it would change the box
// outside the run's isolation, as "workflow: job: step". A run with any is not
// a verdict on the tree: a step that never ran judged nothing.
func (s *Summary) Refused() []string {
	var out []string
	for _, j := range s.Jobs {
		for _, st := range j.Refused {
			out = append(out, j.Workflow+": "+j.ID+": "+st)
		}
	}
	return out
}
