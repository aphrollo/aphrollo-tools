package ghworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Local CI runs on the box the developer works on, sometimes a shared one: a
// vite build that takes five minutes alone hit its thirty-minute deadline when
// every job ran at once at normal priority (#1103). Jobs run one at a time, in
// needs order, below normal priority, unless a limit says more may run at once.

// maxInFlight is the most jobs that were running at once, from a file the jobs
// append "start" and "end" to.
func maxInFlight(events string) int {
	cur, most := 0, 0
	for _, e := range strings.Fields(events) {
		switch e {
		case "start":
			cur++
		case "end":
			cur--
		}
		most = max(most, cur)
	}
	return most
}

const timedJob = `
    steps:
      - run: |
          echo start >> events.txt
          sleep 0.3
          echo end >> events.txt`

func TestRun_JobsRunOneAtATimeByDefault(t *testing.T) {
	src := "on: pull_request\njobs:\n  a:" + timedJob + "\n  b:" + timedJob + "\n  c:" + timedJob + "\n"
	sum, out, dir := runFlow(t, src)
	if sum.Failed() || sum.Count(ResultSuccess) != 3 {
		t.Fatalf("run failed:\n%s", out)
	}
	if got := maxInFlight(readFile(t, filepath.Join(dir, "events.txt"))); got != 1 {
		t.Errorf("%d jobs ran at once with no limit given, want 1", got)
	}
}

// With a limit of two, a and b run together (each waits, bounded, for the
// other's marker) and c starts only once one of them is done: a job takes the
// next free slot, earliest in needs order first.
func TestRun_ALimitOfTwoRunsTwoJobsTogetherAndHoldsTheThirdBack(t *testing.T) {
	meet := func(me, other string) string {
		return `
    steps:
      - run: |
          touch ` + me + `.up
          for i in $(seq 1 100); do [ -f ` + other + `.up ] && break; sleep 0.1; done
          [ -f ` + other + `.up ] || { echo "` + other + ` never ran beside ` + me + `"; exit 1; }
          touch ` + me + `.done`
	}
	sum, out, _ := runFlow(t, "on: pull_request\njobs:\n  a:"+meet("a", "b")+"\n  b:"+meet("b", "a")+`
  c:
    steps:
      - run: test -f a.done || test -f b.done
`, func(o *Options) { o.Jobs = 2 })
	if sum.Failed() || sum.Count(ResultSuccess) != 3 {
		t.Fatalf("a and b did not run together, or c started before a slot was free:\n%s", out)
	}
}

func TestRun_AParallelRunStillWaitsForNeedsAndKeepsTheSummaryInNeedsOrder(t *testing.T) {
	const src = `
on: pull_request
jobs:
  late:
    needs: first
    steps:
      - run: test -f first.txt
  first:
    steps:
      - run: sleep 0.3 && echo done > first.txt
  other:
    steps:
      - run: echo other
`
	orderOf := func(jobs int) string {
		sum, out, _ := runFlow(t, src, func(o *Options) { o.Jobs = jobs })
		if sum.Failed() {
			t.Fatalf("with %d jobs at once, a job that needs another started before it finished:\n%s", jobs, out)
		}
		var ids []string
		for _, j := range sum.Jobs {
			ids = append(ids, j.ID)
		}
		return strings.Join(ids, ",")
	}
	if serial := orderOf(1); serial != "first,other,late" {
		t.Fatalf("serial summary order = %s, want first,other,late (needs order, file order among the ready)", serial)
	}
	if parallel := orderOf(4); parallel != "first,other,late" {
		t.Errorf("parallel summary order = %s, want the order a serial run gives: first,other,late", parallel)
	}
}

func TestRun_AFailedNeedSkipsItsDependentsInAParallelRunToo(t *testing.T) {
	sum, _, _ := runFlow(t, `
on: pull_request
jobs:
  broken:
    steps:
      - run: exit 3
  dependent:
    needs: broken
    steps:
      - run: echo never
  fine:
    steps:
      - run: echo ok
`, func(o *Options) { o.Jobs = 3 })
	if r := result(t, sum, "dependent"); r.Result != ResultSkipped || !strings.Contains(r.Detail, "needs broken") {
		t.Errorf("dependent = %+v, want skipped because broken failed", r)
	}
	if result(t, sum, "fine").Result != ResultSuccess || result(t, sum, "broken").Result != ResultFailure {
		t.Errorf("summary = %+v", sum.Jobs)
	}
}

func TestRun_ParallelJobsPrintWholeLinesEachNamingItsJob(t *testing.T) {
	line := func(c string) string { return strings.Repeat(c, 80) }
	src := `
on: pull_request
jobs:
  a:
    steps:
      - run: for i in $(seq 1 150); do echo ` + line("a") + `; done
  b:
    steps:
      - run: for i in $(seq 1 150); do echo ` + line("b") + `; done
`
	_, out, _ := runFlow(t, src, func(o *Options) { o.Jobs = 2 })
	counts := map[string]int{}
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		switch {
		case strings.Contains(l, "[run]"):
		case l == "[a] "+line("a"):
			counts["a"]++
		case l == "[b] "+line("b"):
			counts["b"]++
		case strings.Contains(l, strings.Repeat("a", 20)) || strings.Contains(l, strings.Repeat("b", 20)):
			t.Fatalf("a job's output line came without its job's name or was cut: %q", l)
		}
	}
	if counts["a"] != 150 || counts["b"] != 150 {
		t.Errorf("whole prefixed lines: a=%d b=%d, want 150 each", counts["a"], counts["b"])
	}
	if !strings.Contains(out, "[a] ci run: job a: success") || !strings.Contains(out, "[b] ci run: job b: success") {
		t.Errorf("each job's own verdict line must carry its name:\n%s", out)
	}
}

func TestRun_ASerialRunPrintsNoJobPrefixes(t *testing.T) {
	_, out, _ := runFlow(t, "on: pull_request\njobs:\n  a:\n    steps:\n      - run: echo hello-a\n")
	if strings.Contains(out, "[a] ") || !strings.Contains(out, "\nhello-a\n") {
		t.Errorf("a serial run's output must be the plain output:\n%s", out)
	}
}

func TestRun_AParallelJobsLastWordsWithNoNewlineAreNotLost(t *testing.T) {
	_, out, _ := runFlow(t, "on: pull_request\njobs:\n  a:\n    steps:\n      - run: printf last-words\n  b:\n    steps:\n      - run: echo b\n",
		func(o *Options) { o.Jobs = 2 })
	if !strings.Contains(out, "[a] last-words\n") {
		t.Errorf("output with no final newline was lost or run into the next line:\n%s", out)
	}
}

func TestRun_EveryStepRunsBelowNormalPriority(t *testing.T) {
	prioProbeOnPath(t)
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - run: prioprobe >> prio.txt
      - run: prioprobe >> prio.txt
`)
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	if got := readFile(t, filepath.Join(dir, "prio.txt")); got != "low\nlow\n" {
		t.Errorf("each step reported its priority as %q, want low for both", got)
	}
	if !strings.Contains(out, "ci run: priority:") {
		t.Errorf("the priority the steps run at was not printed:\n%s", out)
	}
}

func TestRun_AStepThatHitsItsTimeoutIsNamedWithTheLimitAndHowToRaiseIt(t *testing.T) {
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - name: vite build
        run: while :; do :; done
`, func(o *Options) { o.StepTimeout = 500 * time.Millisecond })
	if r := result(t, sum, "j"); r.Result != ResultFailure || r.Detail != "step timed out after 500ms: vite build" {
		t.Errorf("j = %+v, want the failure to name the step and the limit", r)
	}
	if !strings.Contains(out, "vite build: stopped after 500ms") || !strings.Contains(out, "--ci-timeout") || !strings.Contains(out, "ci-timeout in aphrollo.toml") {
		t.Errorf("the output must name the step, the limit and the settings that raise it:\n%s", out)
	}
}

func TestRun_AStepStoppedByTheRunsDeadlineOrACancelIsNotBlamedOnItsOwnTimeout(t *testing.T) {
	src := "on: pull_request\njobs:\n  j:\n    steps:\n      - name: long step\n        run: while :; do :; done\n"
	for name, tc := range map[string]struct {
		ctx  func() (context.Context, context.CancelFunc)
		want string
	}{
		"a deadline": {func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 500*time.Millisecond)
		}, "the run's deadline"},
		"a cancel": {func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(500*time.Millisecond, cancel)
			return ctx, cancel
		}, "cancelled"},
	} {
		wf, _, err := Parse("ci.yml", src)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := tc.ctx()
		var out strings.Builder
		sum, err := Run(ctx, []*Workflow{wf}, Options{Dir: t.TempDir(), Out: &out, StepTimeout: time.Hour})
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r := result(t, sum, "j"); r.Result != ResultFailure || strings.Contains(r.Detail, "timed out after") {
			t.Errorf("%s: j = %+v, a step cut short by the run is not a step timeout", name, r)
		}
		if !strings.Contains(out.String(), "long step: stopped") || !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: the output must say the run stopped it (%q):\n%s", name, tc.want, out.String())
		}
	}
}

func TestRun_AJobsTimeoutMinutesStopsItsStepAndSaysSo(t *testing.T) {
	prev := jobTimeoutUnit
	jobTimeoutUnit = 400 * time.Millisecond
	t.Cleanup(func() { jobTimeoutUnit = prev })
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  j:
    timeout-minutes: 1
    steps:
      - name: long step
        run: while :; do :; done
`)
	if r := result(t, sum, "j"); r.Result != ResultFailure || strings.Contains(r.Detail, "timed out after") {
		t.Errorf("j = %+v", r)
	}
	if !strings.Contains(out, "long step: stopped when the job's timeout-minutes (1) ran out") {
		t.Errorf("the job's own limit must be named:\n%s", out)
	}
}

func TestOptions_JobsDefaultsToOneAndKeepsWhatIsGiven(t *testing.T) {
	for given, want := range map[int]int{0: 1, -4: 1, 1: 1, 2: 2, 16: 16} {
		if got := (Options{Jobs: given}).withDefaults().Jobs; got != want {
			t.Errorf("Jobs %d became %d, want %d", given, got, want)
		}
	}
}

func TestOptionsDescribe_SaysHowManyJobsRunAndHowLongAStepMayTake(t *testing.T) {
	var serial, parallel strings.Builder
	Options{Jobs: 1, StepTimeout: 45 * time.Minute}.describe(&serial)
	Options{Jobs: 3, StepTimeout: time.Hour}.describe(&parallel)
	for _, want := range []string{"ci run: priority: ", "jobs: one at a time, in needs order", "each step stops after 45m0s", "--ci-jobs", "ci-timeout in aphrollo.toml"} {
		if !strings.Contains(serial.String(), want) {
			t.Errorf("a serial run's description lacks %q:\n%s", want, serial.String())
		}
	}
	if !strings.Contains(parallel.String(), "jobs: up to 3 at once") || !strings.Contains(parallel.String(), "each step stops after 1h0m0s") {
		t.Errorf("a parallel run's description:\n%s", parallel.String())
	}
}

func TestJobWriter_WholeLinesGoOutPrefixedAndTheRestWaitsForItsNewline(t *testing.T) {
	var out strings.Builder
	w := &jobWriter{mux: &lineMux{w: &out}, prefix: "[x] "}
	for _, chunk := range []string{"ab", "c\nd", "\n\n", "tail"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if want := "[x] abc\n[x] d\n[x] \n"; out.String() != want {
		t.Errorf("before the flush: %q, want %q (a blank line is a line; the tail waits)", out.String(), want)
	}
	w.flush()
	if want := "[x] abc\n[x] d\n[x] \n[x] tail\n"; out.String() != want {
		t.Errorf("after the flush: %q, want %q", out.String(), want)
	}
	w.flush()
	if want := "[x] abc\n[x] d\n[x] \n[x] tail\n"; out.String() != want {
		t.Errorf("a second flush wrote %q", out.String())
	}
}

func TestStopReason_NamesWhatEndedTheStep(t *testing.T) {
	expired := func(parent context.Context) context.Context {
		ctx, cancel := context.WithTimeout(parent, time.Nanosecond)
		t.Cleanup(cancel)
		<-ctx.Done()
		return ctx
	}
	cancelled := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	failed := context.DeadlineExceeded // stands for the step's own error
	own := &jobRun{opt: Options{StepTimeout: 7 * time.Minute}, job: &Job{}}
	capped := &jobRun{opt: Options{StepTimeout: 7 * time.Minute}, job: &Job{TimeoutMin: 3}}

	if got := own.stopReason(context.Background(), context.Background(), failed); got != failed {
		t.Errorf("a step that failed on its own = %v, want its own error back", got)
	}
	var timeout *stepTimeoutError
	if got := own.stopReason(context.Background(), expired(context.Background()), failed); !errors.As(got, &timeout) || timeout.limit != 7*time.Minute {
		t.Errorf("a step past its own limit = %v, want a stepTimeoutError of 7m", got)
	}
	for name, tc := range map[string]struct {
		r    *jobRun
		ctx  context.Context
		want string
	}{
		"the job's timeout-minutes": {capped, expired(context.Background()), "the job's timeout-minutes (3) ran out"},
		"the run's deadline":        {own, expired(context.Background()), "the run's deadline passed"},
		"a cancel":                  {own, cancelled(), "the run was cancelled"},
		"a cancel in a capped job":  {capped, cancelled(), "the run was cancelled"},
	} {
		got := tc.r.stopReason(tc.ctx, expired(tc.ctx), failed)
		if got == nil || !strings.Contains(got.Error(), tc.want) || errors.As(got, &timeout) {
			t.Errorf("%s: stopReason = %v, want it to say %q and not blame the step's own limit", name, got, tc.want)
		}
	}
}

func TestFailureDetail_NamesTheStepAndTheLimitOnlyForATimeout(t *testing.T) {
	st := &Step{Name: "vite build"}
	r := &jobRun{}
	if got := r.failureDetail(st); got != "step failed: vite build" {
		t.Errorf("failureDetail = %q", got)
	}
	r.timedOut = 30 * time.Minute
	if got := r.failureDetail(st); got != "step timed out after 30m0s: vite build" {
		t.Errorf("failureDetail after a timeout = %q", got)
	}
}

func TestRun_ASerialStepsLastWordsWithNoNewlineDoNotRunIntoTheNextLine(t *testing.T) {
	_, out, _ := runFlow(t, "on: pull_request\njobs:\n  a:\n    steps:\n      - run: printf last-words\n      - run: echo next\n")
	if !strings.Contains(out, "last-words\n") || strings.Contains(out, "last-wordsci run:") || strings.Contains(out, "last-words  [run]") {
		t.Errorf("a step's unterminated last line ran into what followed:\n%s", out)
	}
}

func TestLineEnder_OnlyAnOpenLineGetsItsNewline(t *testing.T) {
	var out strings.Builder
	l := &lineEnder{w: &out}
	l.flush()
	if out.String() != "" {
		t.Errorf("flush on nothing wrote %q", out.String())
	}
	l.Write([]byte("whole\n"))
	l.flush()
	l.Write([]byte("open"))
	l.Write(nil)
	l.flush()
	l.flush()
	if want := "whole\nopen\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
}

func TestInFlight_CountsWhatStartedAndHasNotEnded(t *testing.T) {
	started := []bool{true, true, false, true}
	done := []bool{true, false, false, false}
	if got := inFlight(started, done); got != 2 {
		t.Errorf("inFlight = %d, want 2 (jobs 1 and 3)", got)
	}
	if got := inFlight(nil, nil); got != 0 {
		t.Errorf("inFlight of nothing = %d", got)
	}
}

func TestRun_AToleratedTimeoutDoesNotLabelALaterStepThatFailsBeforeItRuns(t *testing.T) {
	sum, _, _ := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - name: slow but allowed
        continue-on-error: true
        run: while :; do :; done
      - name: broken expression
        run: echo ${{ nosuch() }}
`, func(o *Options) { o.StepTimeout = 400 * time.Millisecond })
	r := result(t, sum, "j")
	if r.Result != ResultFailure || r.Detail != "step failed: broken expression" {
		t.Errorf("j = %+v, want the failure named for the step that failed, not the earlier timeout", r)
	}
}
