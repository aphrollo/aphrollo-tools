package ghworkflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runFlow parses a workflow, runs it in a fresh directory and returns the
// summary, everything it printed, and the directory.
func runFlow(t *testing.T, src string, tweak ...func(*Options)) (*Summary, string, string) {
	t.Helper()
	wf, onPR, err := Parse("ci.yml", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !onPR {
		t.Fatal("the test workflow does not run on pull_request")
	}
	dir := t.TempDir()
	var out bytes.Buffer
	opt := Options{Dir: dir, Out: &out, StepTimeout: time.Minute, Event: map[string]any{
		"sha": "mergesha", "base_sha": "basesha", "head_sha": "headsha", "head_ref": "lane/x", "base_ref": "main", "repository": "o/r",
	}}
	for _, f := range tweak {
		f(&opt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sum, err := Run(ctx, []*Workflow{wf}, opt)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	return sum, out.String(), dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func result(t *testing.T, s *Summary, job string) JobResult {
	t.Helper()
	for _, j := range s.Jobs {
		if j.ID == job {
			return j
		}
	}
	t.Fatalf("job %s did not run; summary %+v", job, s.Jobs)
	return JobResult{}
}

func TestRun_StepsRunInOrderWithTheirEnvLayersAndTheRunnerVariables(t *testing.T) {
	sum, out, dir := runFlow(t, `
on: pull_request
env:
  A: wf
  B: wf
jobs:
  build:
    runs-on: ubuntu-latest
    env:
      B: job
      C: job
    steps:
      - run: echo "1 $A $B $C" >> log.txt
      - run: echo "2 $A $B $C $D" >> log.txt
        env:
          C: step
          D: ${{ env.A }}-${{ github.event.pull_request.base.sha }}
      - run: echo "3 $CI $GITHUB_ACTIONS $GITHUB_EVENT_NAME $GITHUB_SHA $GITHUB_BASE_REF" >> log.txt
`)
	if sum.Failed() || result(t, sum, "build").Result != ResultSuccess {
		t.Fatalf("run failed:\n%s", out)
	}
	want := "1 wf job job\n2 wf job step wf-basesha\n3 true true pull_request mergesha main\n"
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestRun_AFailedStepStopsTheJobAndSkipsWhatNeedsIt(t *testing.T) {
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  first:
    steps:
      - run: echo one >> log.txt
      - run: exit 3
      - run: echo never >> log.txt
  second:
    needs: first
    steps:
      - run: echo second >> log.txt
  third:
    steps:
      - run: echo third >> log.txt
`)
	if !sum.Failed() {
		t.Fatalf("a failing step did not fail the run:\n%s", out)
	}
	if r := result(t, sum, "first"); r.Result != ResultFailure || !strings.Contains(r.Detail, "exit 3") {
		t.Errorf("first = %+v, want a failure naming the step", r)
	}
	if r := result(t, sum, "second"); r.Result != ResultSkipped || !strings.Contains(r.Detail, "needs first") {
		t.Errorf("second = %+v, want skipped because first failed", r)
	}
	if r := result(t, sum, "third"); r.Result != ResultSuccess {
		t.Errorf("third = %+v, an independent job still runs", r)
	}
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "one\nthird\n" {
		t.Errorf("log = %q, want only the steps that should run", got)
	}
	if sum.Count(ResultSkipped) != 1 || sum.Count(ResultFailure) != 1 || sum.Count(ResultSuccess) != 1 {
		t.Errorf("counts wrong: %+v", sum.Jobs)
	}
}

func TestRun_JobsRunInNeedsOrderNotFileOrder(t *testing.T) {
	_, _, dir := runFlow(t, `
on: pull_request
jobs:
  last:
    needs: [middle]
    steps:
      - run: echo last >> log.txt
  middle:
    needs: first
    steps:
      - run: echo middle >> log.txt
  first:
    steps:
      - run: echo first >> log.txt
`)
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "first\nmiddle\nlast\n" {
		t.Errorf("order = %q", got)
	}
}

func TestRun_RefusesAnUnknownNeedAndACycle(t *testing.T) {
	for name, src := range map[string]string{
		"unknown": "on: pull_request\njobs:\n  a:\n    needs: ghost\n    steps:\n      - run: echo\n",
		"cycle":   "on: pull_request\njobs:\n  a:\n    needs: b\n    steps:\n      - run: echo\n  b:\n    needs: a\n    steps:\n      - run: echo\n",
	} {
		wf, _, err := Parse("ci.yml", src)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Run(context.Background(), []*Workflow{wf}, Options{Dir: t.TempDir()})
		if err == nil {
			t.Errorf("%s: Run succeeded, want a refusal", name)
		}
	}
}

func TestRun_ContinueOnErrorKeepsTheJobGoing(t *testing.T) {
	sum, _, dir := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - id: soft
        continue-on-error: true
        run: exit 1
      - run: echo "${{ steps.soft.outcome }} ${{ steps.soft.conclusion }}" >> log.txt
      - continue-on-error: ${{ false }}
        run: exit 1
      - run: echo unreachable >> log.txt
`)
	if r := result(t, sum, "j"); r.Result != ResultFailure {
		t.Errorf("j = %+v, the hard failure after the soft one must fail the job", r)
	}
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "failure success\n" {
		t.Errorf("log = %q", got)
	}
}

func TestRun_IfConditionsAndStatusFunctionsChooseTheSteps(t *testing.T) {
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - if: github.event_name == 'push'
        run: echo push >> log.txt
      - if: ${{ github.event_name == 'pull_request' }}
        run: echo pr >> log.txt
      - if: failure()
        run: echo not-yet >> log.txt
      - run: exit 1
      - run: echo skipped-after-failure >> log.txt
      - if: failure()
        run: echo on-failure >> log.txt
      - if: always()
        run: echo always >> log.txt
`)
	if !sum.Failed() {
		t.Fatal("the run should fail")
	}
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "pr\non-failure\nalways\n" {
		t.Errorf("log = %q\n%s", got, out)
	}
	if !strings.Contains(out, "[skip] echo push >> log.txt (if: github.event_name == 'push')") {
		t.Errorf("a skipped step must be printed with its condition:\n%s", out)
	}
	if !strings.Contains(out, "(if: an earlier step failed)") {
		t.Errorf("a step skipped for an earlier failure must say so:\n%s", out)
	}
}

func TestRun_AnUnevaluableConditionIsPrintedAndTheStepRuns(t *testing.T) {
	_, out, dir := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - if: mystery(1)
        run: echo ran >> log.txt
`)
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "ran\n" {
		t.Errorf("the step did not run: %q", got)
	}
	if !strings.Contains(out, "mystery") || !strings.Contains(out, "could not be evaluated") {
		t.Errorf("the unevaluable expression was not printed:\n%s", out)
	}
}

func TestRun_UsesStepsAreNeverExecutedAndAlwaysNamed(t *testing.T) {
	_, out, _ := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-node@v4
      - uses: some/other-action@v1
      - run: echo done
`)
	for _, want := range []string{
		"[skip] uses: actions/checkout@v4 (the throwaway worktree of the merge result is the checkout)",
		"[skip] uses: actions/setup-node@v4 (assumed satisfied by this box's toolchain)",
		"[skip] uses: some/other-action@v1 (actions are not executed locally)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestRun_StepsRunInTheirWorkingDirectory(t *testing.T) {
	wf, _, err := Parse("ci.yml", `
on: pull_request
defaults:
  run:
    working-directory: wfdir
jobs:
  j:
    defaults:
      run:
        working-directory: jobdir
    steps:
      - run: basename "$PWD" >> ../log.txt
        working-directory: stepdir
      - run: basename "$PWD" >> ../log.txt
      - run: basename "$PWD" >> log.txt
        working-directory: ${{ github.event_name }}-dir
  w:
    steps:
      - run: basename "$PWD" >> ../wlog.txt
`)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, d := range []string{"stepdir", "jobdir", "wfdir", "pull_request-dir"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	sum, err := Run(context.Background(), []*Workflow{wf}, Options{Dir: dir, Out: &out, StepTimeout: time.Minute})
	if err != nil || sum.Failed() {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "stepdir\njobdir\n" {
		t.Errorf("working directories = %q, want the step's, then the job's", got)
	}
	if got := readFile(t, filepath.Join(dir, "wlog.txt")); got != "wfdir\n" {
		t.Errorf("the workflow default directory = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "pull_request-dir", "log.txt")); got != "pull_request-dir\n" {
		t.Errorf("an expression in working-directory was not evaluated: %q", got)
	}
}
