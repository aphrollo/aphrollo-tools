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

func TestRun_StepOutputsFlowIntoLaterStepsAndJobOutputsIntoDependentJobs(t *testing.T) {
	_, out, dir := runFlow(t, `
on: pull_request
jobs:
  plan:
    outputs:
      code: ${{ steps.filter.outputs.code }}
      multi: ${{ steps.filter.outputs.multi }}
    steps:
      - id: filter
        run: |
          echo "code=true" >> "$GITHUB_OUTPUT"
          {
            echo "multi<<EOF"
            echo "line one"
            echo "line two"
            echo "EOF"
          } >> "$GITHUB_OUTPUT"
      - if: steps.filter.outputs.code == 'true'
        run: echo "gated ${{ steps.filter.outputs.code }}" >> log.txt
  use:
    needs: plan
    if: needs.plan.outputs.code == 'true'
    steps:
      - run: echo "${{ needs.plan.result }} ${{ needs.plan.outputs.code }}" >> log.txt
      - run: printf '%s\n' "${{ needs.plan.outputs.multi }}" >> multi.txt
  never:
    needs: plan
    if: needs.plan.outputs.code == 'false'
    steps:
      - run: echo never >> log.txt
`)
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "gated true\nsuccess true\n" {
		t.Errorf("log = %q\n%s", got, out)
	}
	if got := readFile(t, filepath.Join(dir, "multi.txt")); got != "line one\nline two\n" {
		t.Errorf("a multi-line output was lost: %q", got)
	}
	if !strings.Contains(out, "[skip] job never: if: needs.plan.outputs.code == 'false' is false") {
		t.Errorf("a job skipped by its if must say so:\n%s", out)
	}
}

func TestRun_GithubEnvAndPathCarryToLaterSteps(t *testing.T) {
	_, out, dir := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - run: |
          echo "FROM_ENV=hello" >> "$GITHUB_ENV"
          mkdir -p tools
          printf '#!/bin/sh\necho tool-output\n' > tools/mytool
          chmod +x tools/mytool
          echo "$PWD/tools" >> "$GITHUB_PATH"
      - run: mytool >> log.txt
      - run: echo "$FROM_ENV ${{ env.FROM_ENV }}" >> log.txt
`)
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "tool-output\nhello hello\n" {
		t.Errorf("log = %q\n%s", got, out)
	}
}

func TestRun_MatrixRunsTheFirstCombinationOnlyAndSaysSo(t *testing.T) {
	_, out, dir := runFlow(t, `
on: pull_request
jobs:
  plan:
    outputs:
      shards: ${{ steps.p.outputs.shards }}
    steps:
      - id: p
        run: echo 'shards=[7,8,9]' >> "$GITHUB_OUTPUT"
  fan:
    needs: plan
    strategy:
      matrix:
        shard: ${{ fromJSON(needs.plan.outputs.shards) }}
        os: [linux, mac]
    steps:
      - run: echo "${{ matrix.shard }} ${{ matrix.os }}" >> log.txt
  inc:
    strategy:
      matrix:
        include:
          - name: linux
            runner: ubuntu
          - name: other
            runner: x
    steps:
      - run: echo "${{ matrix.name }} ${{ matrix.runner }}" >> log.txt
`)
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "7 linux\nlinux ubuntu\n" {
		t.Errorf("log = %q\n%s", got, out)
	}
	if !strings.Contains(out, "matrix: ran the first combination only (os=linux, shard=7)") {
		t.Errorf("the matrix reduction was not printed:\n%s", out)
	}
	if !strings.Contains(out, "(name=linux, runner=ubuntu)") {
		t.Errorf("the include reduction was not printed:\n%s", out)
	}
}

func TestRun_AnEmptyMatrixSkipsTheJobAndAnUnreadableOneFailsIt(t *testing.T) {
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  empty:
    strategy:
      matrix:
        shard: ${{ fromJSON('[]') }}
    steps:
      - run: echo never
  literal-empty:
    strategy:
      matrix:
        shard: []
    steps:
      - run: echo never
  bad:
    strategy:
      matrix: ${{ fromJSON('{}') }}
    steps:
      - run: echo never
`)
	if r := result(t, sum, "empty"); r.Result != ResultSkipped || !strings.Contains(r.Detail, "no combinations") {
		t.Errorf("empty = %+v", r)
	}
	if r := result(t, sum, "literal-empty"); r.Result != ResultSkipped {
		t.Errorf("literal-empty = %+v", r)
	}
	if r := result(t, sum, "bad"); r.Result != ResultFailure || !strings.Contains(out, "matrix") {
		t.Errorf("bad = %+v\n%s", r, out)
	}
}

func TestRun_JobsLocalCINeverRunsAreSkippedAndNamed(t *testing.T) {
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  db:
    services:
      postgres:
        image: postgres
    steps:
      - run: echo never
  boxed:
    container: node:20
    steps:
      - run: echo never
  reuse:
    uses: ./.github/workflows/other.yml
  plain:
    steps:
      - run: echo ok
`)
	for id, want := range map[string]string{"db": "services", "boxed": "container", "reuse": "reusable workflow"} {
		r := result(t, sum, id)
		if r.Result != ResultSkipped || !strings.Contains(r.Detail, want) {
			t.Errorf("%s = %+v, want skipped naming %q", id, r, want)
		}
		if !strings.Contains(out, "[skip] job "+id) {
			t.Errorf("the skip of %s was not printed:\n%s", id, out)
		}
	}
	if result(t, sum, "plain").Result != ResultSuccess {
		t.Error("the plain job should have run")
	}
}

func TestRun_AStatusFunctionOnAJobLetsItRunAfterAFailedNeed(t *testing.T) {
	sum, _, dir := runFlow(t, `
on: pull_request
jobs:
  broken:
    steps:
      - run: exit 1
  cleanup:
    needs: broken
    if: ${{ always() }}
    steps:
      - run: echo cleaned >> log.txt
  report:
    needs: broken
    if: failure()
    steps:
      - run: echo reported >> log.txt
  gated:
    needs: broken
    steps:
      - run: echo gated >> log.txt
  plainif:
    needs: broken
    if: github.event_name == 'pull_request'
    steps:
      - run: echo plainif >> log.txt
`)
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "cleaned\nreported\n" {
		t.Errorf("log = %q", got)
	}
	if result(t, sum, "gated").Result != ResultSkipped || result(t, sum, "plainif").Result != ResultSkipped {
		t.Error("a job with no status function must skip after a failed need")
	}
}

func TestRun_AStepThatOutlastsItsTimeoutIsStoppedAndFails(t *testing.T) {
	start := time.Now()
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - run: sleep 30
`, func(o *Options) { o.StepTimeout = 500 * time.Millisecond })
	if r := result(t, sum, "j"); r.Result != ResultFailure {
		t.Fatalf("j = %+v", r)
	}
	if !strings.Contains(out, "stopped after 500ms") {
		t.Errorf("the timeout was not reported:\n%s", out)
	}
	if time.Since(start) > 25*time.Second {
		t.Errorf("the run took %v: the step was not killed at its timeout", time.Since(start))
	}
}

func TestRun_UnsupportedShellsFailLoudlyAndCustomTemplatesRun(t *testing.T) {
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  ps:
    steps:
      - shell: pwsh
        run: Write-Host hi
  custom:
    steps:
      - shell: bash -e {0}
        run: echo custom >> log.txt
  explicit:
    defaults:
      run:
        shell: bash
    steps:
      - run: echo explicit >> log.txt
  sh:
    steps:
      - shell: sh
        run: echo viash >> log.txt
`)
	if r := result(t, sum, "ps"); r.Result != ResultFailure || !strings.Contains(out, "pwsh is not supported locally") {
		t.Errorf("ps = %+v\n%s", r, out)
	}
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "custom\nexplicit\nviash\n" {
		t.Errorf("log = %q", got)
	}
}

func TestRun_UnresolvedExpressionsAreListedNotHidden(t *testing.T) {
	_, out, _ := runFlow(t, `
on: pull_request
jobs:
  j:
    steps:
      - run: echo "[${{ secrets.TOKEN }}]" >> log.txt
`)
	if !strings.Contains(out, "unresolved: secrets.TOKEN") {
		t.Errorf("an empty secret must be listed:\n%s", out)
	}
}

func TestRun_BadExpressionsInRunEnvAndOutputsFailLoudly(t *testing.T) {
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  run-bad:
    steps:
      - run: echo ${{ nosuch() }}
  env-bad:
    steps:
      - env:
          X: ${{ nosuch() }}
        run: echo hi
  jobenv-bad:
    env:
      X: ${{ nosuch() }}
    steps:
      - run: echo hi
  out-bad:
    outputs:
      o: ${{ nosuch() }}
    steps:
      - run: echo hi
`)
	for _, id := range []string{"run-bad", "env-bad", "jobenv-bad"} {
		if r := result(t, sum, id); r.Result != ResultFailure {
			t.Errorf("%s = %+v, want a failure", id, r)
		}
	}
	if !strings.Contains(out, "nosuch") {
		t.Errorf("the bad expression was not named:\n%s", out)
	}
	if !strings.Contains(out, "output o could not be evaluated") {
		t.Errorf("a bad job output must be said:\n%s", out)
	}
}

func TestRun_AJobTimeoutStopsAStepThatOutlastsIt(t *testing.T) {
	wf, _, err := Parse("ci.yml", "on: pull_request\njobs:\n  j:\n    timeout-minutes: 1\n    steps:\n      - run: echo ok\n")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Jobs[0].TimeoutMin != 1 {
		t.Fatalf("timeout-minutes = %d", wf.Jobs[0].TimeoutMin)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the job's context is already over: its step must not run
	var out bytes.Buffer
	dir := t.TempDir()
	wf.Jobs[0].Steps[0].Run = "echo ran >> log.txt"
	sum, err := Run(ctx, []*Workflow{wf}, Options{Dir: dir, Out: &out, StepTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if r := result(t, sum, "j"); r.Result != ResultFailure {
		t.Errorf("a step under a finished context must fail, got %+v\n%s", r, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "log.txt")); err == nil {
		t.Error("the step ran under a finished context: it left log.txt behind")
	}
}
