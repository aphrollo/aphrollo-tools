package ghworkflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// Options is what a run needs besides the workflows themselves. Zero values
// take the defaults named on each field.
type Options struct {
	Dir         string         // the checkout the workflows run in
	Out         io.Writer      // everything the steps print, and what local CI says about them
	Event       map[string]any // github context values: sha, base_sha, head_sha, head_ref, base_ref, repository
	StepTimeout time.Duration  // per step, 30 minutes when zero
	Env         []string       // the base environment, os.Environ() when nil
	Jobs        int            // jobs that may run at once, one at a time in needs order when zero

	iso *isolation // set by Run: what keeps the steps' installs inside the run's scratch
}

// defaultStepTimeout bounds a step that gives none.
const defaultStepTimeout = 30 * time.Minute

// waitDelay is how long a killed step's output pipes get to drain: 10 seconds,
// written in nanoseconds so no arithmetic stands between the reader and it.
const waitDelay time.Duration = 10_000_000_000

// withDefaults fills what a caller left zero.
func (o Options) withDefaults() Options {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.StepTimeout <= 0 {
		o.StepTimeout = defaultStepTimeout
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	o.Jobs = max(o.Jobs, 1)
	return o
}

// Job results.
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
	ResultSkipped = "skipped"
)

// JobResult is how one job ended and, for a skipped or failed one, why.
type JobResult struct {
	Workflow string
	ID       string
	Result   string
	Detail   string
	Refused  []string // steps not run because they would change the box outside the run's isolation
}

// Summary is every job of a run, in the order they ran.
type Summary struct{ Jobs []JobResult }

// Failed reports whether any job failed. A skipped job is not a failure; the
// summary names each one so it is never silent.
func (s *Summary) Failed() bool {
	for _, j := range s.Jobs {
		if j.Result == ResultFailure {
			return true
		}
	}
	return false
}

// Count is the number of jobs with the given result.
func (s *Summary) Count(result string) int {
	n := 0
	for _, j := range s.Jobs {
		if j.Result == result {
			n++
		}
	}
	return n
}

// Run runs every workflow's jobs in needs order. A job's steps stop at its
// first failure; the run goes on to independent jobs, and the summary is red
// if any job is. Nothing is skipped silently: each skipped job, step and
// unevaluable expression is printed.
func Run(ctx context.Context, flows []*Workflow, opt Options) (*Summary, error) {
	opt = opt.withDefaults()
	tmp, err := os.MkdirTemp("", "aphrollo-ci-run-")
	if err != nil {
		return nil, fmt.Errorf("a scratch directory for the run could not be made: %w", err)
	}
	defer func() {
		if err := removeScratch(tmp); err != nil {
			fmt.Fprintf(opt.Out, "ci run: [note] the run's scratch directory %s was not removed: %v\n", tmp, err)
		}
	}()
	opt.Out = &lineEnder{w: opt.Out}
	opt.iso = newIsolation(ctx, tmp, flows, opt.Env)
	opt.iso.describe(opt.Out)
	opt.describe(opt.Out)
	sum := &Summary{}
	for _, wf := range flows {
		fmt.Fprintf(opt.Out, "ci run: workflow %s (%s)\n", wf.Name, wf.File)
		for _, note := range wf.Notes {
			fmt.Fprintf(opt.Out, "  [note] %s\n", note)
		}
		if err := runWorkflow(ctx, wf, opt, tmp, sum); err != nil {
			return sum, fmt.Errorf("%s: %w", wf.File, err)
		}
	}
	return sum, nil
}

// order puts jobs in needs order, keeping file order among the ready ones.
func order(jobs []*Job) ([]*Job, error) {
	byID := map[string]bool{}
	for _, j := range jobs {
		byID[j.ID] = true
	}
	for _, j := range jobs {
		for _, n := range j.Needs {
			if !byID[n] {
				return nil, fmt.Errorf("job %s needs %s, which is not a job of this workflow", j.ID, n)
			}
		}
	}
	done := map[string]bool{}
	var out []*Job
	for range jobs {
		for _, j := range jobs {
			if done[j.ID] || !allDone(j.Needs, done) {
				continue
			}
			done[j.ID] = true
			out = append(out, j)
		}
	}
	if len(out) < len(jobs) {
		return nil, fmt.Errorf("the jobs' needs form a cycle")
	}
	return out, nil
}

func allDone(ids []string, done map[string]bool) bool {
	for _, id := range ids {
		if !done[id] {
			return false
		}
	}
	return true
}

type jobState struct {
	result  string
	outputs map[string]any
}

func runWorkflow(ctx context.Context, wf *Workflow, opt Options, tmp string, sum *Summary) error {
	jobs, err := order(wf.Jobs)
	if err != nil {
		return err
	}
	sum.Jobs = append(sum.Jobs, runJobs(ctx, wf, jobs, opt, tmp)...)
	return nil
}

// jobRun is one job's run: its scope, its step results and its env.
type jobRun struct {
	wf      *Workflow
	job     *Job
	opt     Options
	tmp     string
	states  *stateTable
	steps   map[string]any
	matrix  map[string]any
	env     []string
	envCtx  map[string]any
	outputs map[string]any
	scope   *Scope
	// timedOut is the limit the last step ran into, zero when it did not.
	timedOut time.Duration
}

func (r *jobRun) skip(detail string) JobResult {
	fmt.Fprintf(r.opt.Out, "ci run: [skip] job %s: %s\n", r.job.ID, detail)
	return JobResult{ID: r.job.ID, Result: ResultSkipped, Detail: detail}
}

func (r *jobRun) run(ctx context.Context) JobResult {
	j := r.job
	fmt.Fprintf(r.opt.Out, "ci run: job %s\n", j.ID)
	if j.Unsupported != "" {
		return r.skip(j.Unsupported + " is not run locally")
	}
	r.steps = map[string]any{}
	r.scope = &Scope{Root: r.baseScope(), Status: r.needsStatus()}
	if why := r.needsBlock(); why != "" {
		if cond, err := r.scope.Cond(j.If); err != nil || !cond || !usesStatus(r.scope, j.If) {
			return r.skip(why)
		}
	}
	if err := r.expandMatrix(); err != nil {
		if errors.Is(err, errEmptyMatrix) {
			return r.skip("its matrix has no combinations")
		}
		return r.fail(fmt.Sprintf("matrix: %v", err))
	}
	if j.If != "" {
		run, err := r.scope.Cond(j.If)
		switch {
		case err != nil:
			fmt.Fprintf(r.opt.Out, "  [note] if: %s could not be evaluated (%v): running the job\n", j.If, err)
		case !run:
			return r.skip("if: " + strings.TrimSpace(j.If) + " is false")
		}
	}
	if err := os.MkdirAll(r.tmp, 0o755); err != nil {
		return r.fail(fmt.Sprintf("a scratch directory could not be made: %v", err))
	}
	if err := r.initEnv(); err != nil {
		return r.fail(err.Error())
	}
	jobCtx := ctx
	if j.TimeoutMin > 0 {
		var cancel context.CancelFunc
		jobCtx, cancel = context.WithTimeout(ctx, time.Duration(j.TimeoutMin)*jobTimeoutUnit)
		defer cancel()
	}
	res := r.runSteps(jobCtx)
	r.finishOutputs()
	return res
}

func (r *jobRun) fail(detail string) JobResult {
	fmt.Fprintf(r.opt.Out, "ci run: job %s: %s\n", r.job.ID, detail)
	return JobResult{ID: r.job.ID, Result: ResultFailure, Detail: detail}
}

// needsStatus is the status a job-level status function answers from: failure
// once any job this one needs has failed.
func (r *jobRun) needsStatus() string {
	for _, n := range r.job.Needs {
		if st := r.states.get(n); st != nil && st.result == ResultFailure {
			return ResultFailure
		}
	}
	return ResultSuccess
}

// needsBlock is why a job cannot start because of the jobs it needs: one of
// them failed or was skipped.
func (r *jobRun) needsBlock() string {
	for _, n := range r.job.Needs {
		if st := r.states.get(n); st != nil && st.result != ResultSuccess {
			return fmt.Sprintf("needs %s, which %s", n, st.result)
		}
	}
	return ""
}

// usesStatus reports whether an if: calls a status function, which lets a job
// run after a failed or skipped need.
func usesStatus(s *Scope, expr string) bool {
	e := strings.TrimSpace(expr)
	e = strings.TrimSuffix(strings.TrimPrefix(e, "${{"), "}}")
	_, used, err := s.Eval(strings.TrimSpace(e))
	return err == nil && used
}

func (r *jobRun) baseScope() map[string]any {
	needs := map[string]any{}
	for _, n := range r.job.Needs {
		st := r.states.get(n)
		if st == nil {
			continue
		}
		needs[n] = map[string]any{"result": st.result, "outputs": st.outputs}
	}
	ev := r.opt.Event
	pr := map[string]any{
		"draft": false,
		"base":  map[string]any{"sha": ev["base_sha"], "ref": ev["base_ref"]},
		"head":  map[string]any{"sha": ev["head_sha"], "ref": ev["head_ref"]},
	}
	return map[string]any{
		"github": map[string]any{
			"event_name": "pull_request", "sha": ev["sha"], "ref": "refs/pull/0/merge",
			"base_ref": ev["base_ref"], "head_ref": ev["head_ref"], "repository": ev["repository"],
			"workspace": r.opt.Dir, "run_id": "1", "run_attempt": "1", "actor": "local",
			"workflow": r.wf.Name, "token": "", "event": map[string]any{"pull_request": pr},
		},
		"runner":  map[string]any{"os": hostOS(), "temp": r.tmp},
		"needs":   needs,
		"env":     map[string]any{},
		"steps":   map[string]any{},
		"secrets": map[string]any{}, "vars": map[string]any{}, "inputs": map[string]any{},
		"job": map[string]any{"status": ResultSuccess},
	}
}

// ctxFor refreshes the scope's changing contexts before an expression runs.
func (r *jobRun) ctxFor(status string, env map[string]any) *Scope {
	r.scope.Status = status
	r.scope.Root["steps"] = r.steps
	r.scope.Root["env"] = env
	r.scope.Root["matrix"] = r.matrix
	r.scope.Root["job"] = map[string]any{"status": status}
	return r.scope
}

// finishOutputs evaluates the job's outputs from its steps' outputs.
func (r *jobRun) finishOutputs() {
	r.outputs = map[string]any{}
	sc := r.ctxFor(ResultSuccess, map[string]any{})
	for _, kv := range r.job.Outputs {
		v, err := sc.Interpolate(kv.Val)
		if err != nil {
			fmt.Fprintf(r.opt.Out, "  [note] output %s could not be evaluated (%v): left empty\n", kv.Key, err)
		}
		r.outputs[kv.Key] = v
	}
}

func (r *jobRun) runSteps(ctx context.Context) JobResult {
	status := ResultSuccess
	failedAt := ""
	var refusedSteps []string
	for _, st := range r.job.Steps {
		sc := r.ctxFor(status, r.envMap())
		run, err := sc.Cond(st.If)
		if err != nil {
			fmt.Fprintf(r.opt.Out, "  [note] if: %s could not be evaluated (%v): running the step\n", st.If, err)
			run = true
		}
		if !run {
			fmt.Fprintf(r.opt.Out, "  [skip] %s (if: %s)\n", st.Label(), skipReason(st.If))
			r.recordStep(st, "skipped", "skipped", nil)
			continue
		}
		if st.Uses != "" {
			fmt.Fprintf(r.opt.Out, "  [skip] uses: %s (%s)\n", st.Uses, usesReason(st.Uses))
			r.recordStep(st, "skipped", "skipped", nil)
			continue
		}
		ok, refused := r.runStep(ctx, st)
		if refused {
			refusedSteps = append(refusedSteps, st.Label())
			continue
		}
		if !ok && failedAt == "" {
			if r.tolerated(st) {
				if rec, ok := r.steps[st.ID].(map[string]any); ok {
					rec["conclusion"] = ResultSuccess
				}
				continue
			}
			status = ResultFailure
			failedAt = r.failureDetail(st)
		}
	}
	for _, n := range r.scope.Notes {
		fmt.Fprintf(r.opt.Out, "  [note] %s\n", n)
	}
	if failedAt != "" {
		return JobResult{ID: r.job.ID, Result: ResultFailure, Detail: failedAt, Refused: refusedSteps}
	}
	return JobResult{ID: r.job.ID, Result: ResultSuccess, Refused: refusedSteps}
}

func skipReason(cond string) string {
	if strings.TrimSpace(cond) == "" {
		return "an earlier step failed"
	}
	return strings.TrimSpace(cond)
}

func usesReason(uses string) string {
	switch name, _, _ := strings.Cut(uses, "@"); {
	case name == "actions/checkout":
		return "the throwaway worktree of the merge result is the checkout"
	case strings.HasPrefix(name, "actions/setup-") || strings.Contains(name, "/setup-"):
		return "assumed satisfied by this box's toolchain"
	}
	return "actions are not executed locally"
}

// tolerated reports whether a failed step has continue-on-error: true.
func (r *jobRun) tolerated(st *Step) bool {
	if st.ContinueOnError == "" {
		return false
	}
	v, err := r.scope.Interpolate(st.ContinueOnError)
	return err == nil && strings.EqualFold(strings.TrimSpace(v), "true")
}

// runStep runs one run: step and records its outcome. It reports success.
func (r *jobRun) runStep(ctx context.Context, st *Step) (ok, refused bool) {
	r.timedOut = 0 // before anything below can fail: only this step's own timeout labels it
	fmt.Fprintf(r.opt.Out, "  [run] %s\n", st.Label())
	sc := r.ctxFor(r.scope.Status, r.envMap())
	script, err := sc.Interpolate(st.Run)
	if err != nil {
		fmt.Fprintf(r.opt.Out, "  [fail] %v\n", err)
		r.recordStep(st, ResultFailure, ResultFailure, nil)
		return false, false
	}
	stepEnv, err := r.stepEnv(sc, st)
	if err != nil {
		fmt.Fprintf(r.opt.Out, "  [fail] %v\n", err)
		r.recordStep(st, ResultFailure, ResultFailure, nil)
		return false, false
	}
	if why := r.refusal(st, script); why != "" {
		fmt.Fprintf(r.opt.Out, "  [skip] %s (refused: %s)\n", st.Label(), why)
		r.recordStep(st, "skipped", "skipped", nil)
		return true, true
	}
	files, err := r.stepFiles()
	if err != nil {
		fmt.Fprintf(r.opt.Out, "  [fail] %v\n", err)
		r.recordStep(st, ResultFailure, ResultFailure, nil)
		return false, false
	}
	runErr := r.exec(ctx, st, script, r.opt.iso.apply(append(stepEnv, files.env2()...)))
	var timeout *stepTimeoutError
	if errors.As(runErr, &timeout) {
		r.timedOut = timeout.limit
	}
	outputs := files.collect(r)
	outcome := ResultSuccess
	if runErr != nil {
		fmt.Fprintf(r.opt.Out, "  [fail] %s: %v\n", st.Label(), runErr)
		outcome = ResultFailure
	}
	r.recordStep(st, outcome, outcome, outputs)
	return runErr == nil, false
}

func (r *jobRun) exec(ctx context.Context, st *Step, script string, env []string) error {
	shell := r.shellOf(st)
	file := filepath.Join(r.tmp, fmt.Sprintf("step-%d.sh", st.Line))
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		return fmt.Errorf("writing the step script: %w", err)
	}
	argv, err := shellArgv(shell, file)
	if err != nil {
		return err
	}
	argv = lowPriorityArgv(r.opt.iso.interpreter(shell, argv))
	dir := r.opt.Dir
	if wd := firstNonEmpty(st.WorkDir, r.job.WorkDir, r.wf.WorkDir); wd != "" {
		wd, _ = r.scope.Interpolate(wd)
		dir = filepath.Join(r.opt.Dir, wd)
		if filepath.IsAbs(wd) {
			dir = wd
		}
	}
	stepCtx, cancel := context.WithTimeout(ctx, r.opt.StepTimeout)
	defer cancel()
	cmd := exec.CommandContext(stepCtx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdout, cmd.Stderr = r.opt.Out, r.opt.Out
	cmd.SysProcAttr = lowPriorityAttrs(proc.TreeAttrs())
	cmd.Cancel = func() error { return proc.KillTree(cmd.Process.Pid) }
	cmd.WaitDelay = waitDelay
	err = cmd.Run()
	if f, ok := r.opt.Out.(interface{ flush() }); ok {
		f.flush()
	}
	if err != nil {
		return r.stopReason(ctx, stepCtx, err)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
