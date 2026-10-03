package ghworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_ReadsTheJobModelAndTheTriggerForms(t *testing.T) {
	for _, c := range []struct {
		name, on string
		want     bool
		notes    int
	}{
		{"scalar", "on: pull_request", true, 0},
		{"other scalar", "on: push", false, 0},
		{"list", "on: [push, pull_request]", true, 0},
		{"list without", "on: [push, workflow_dispatch]", false, 0},
		{"map", "on:\n  push:\n    branches: [main]\n  pull_request:", true, 0},
		{"map with merge_group beside pull_request", "on:\n  merge_group:\n    types: [checks_requested]\n  pull_request:\n    types: [opened]", true, 1},
		{"merge_group alone is not pull_request", "on:\n  merge_group:\n    types: [checks_requested]", false, 0},
		{"map without", "on:\n  push:\n    branches: [main]", false, 0},
		{"map with filters", "on:\n  pull_request:\n    branches: [main]\n    paths: ['src/**']\n    types: [opened]", true, 3},
		{"target is not pull_request", "on: pull_request_target", false, 0},
		{"missing", "name: x", false, 0},
	} {
		wf, onPR, err := Parse("w.yml", c.on+"\njobs:\n  j:\n    steps:\n      - run: echo\n")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if onPR != c.want {
			t.Errorf("%s: onPR = %v, want %v", c.name, onPR, c.want)
		}
		if len(wf.Notes) != c.notes {
			t.Errorf("%s: notes = %v, want %d", c.name, wf.Notes, c.notes)
		}
	}
}

func TestParse_FillsEveryJobAndStepField(t *testing.T) {
	wf, _, err := Parse("w.yml", `
name: Pipe
on: pull_request
env:
  A: "1"
defaults:
  run:
    working-directory: web
    shell: bash
jobs:
  build:
    name: Build it
    if: github.event_name == 'pull_request'
    needs: [lint, test]
    timeout-minutes: 7
    env:
      B: two
    outputs:
      out: ${{ steps.s.outputs.x }}
    strategy:
      matrix:
        os: [linux]
    steps:
      - id: s
        name: Named
        if: success()
        working-directory: sub
        shell: sh
        continue-on-error: true
        env:
          C: three
        run: echo hi
      - uses: actions/checkout@v4
      - run: |
          first line
          second
  lint:
    needs: test
    steps:
      - run: echo
  test:
    steps:
      - run: echo
`)
	if err != nil {
		t.Fatal(err)
	}
	if wf.Name != "Pipe" || wf.File != "w.yml" || wf.WorkDir != "web" || wf.Shell != "bash" || len(wf.Env) != 1 || wf.Env[0] != (KV{"A", "1"}) {
		t.Errorf("workflow = %+v", wf)
	}
	b := wf.Jobs[0]
	if b.ID != "build" || b.Name != "Build it" || b.If != "github.event_name == 'pull_request'" || b.TimeoutMin != 7 ||
		strings.Join(b.Needs, ",") != "lint,test" || len(b.Env) != 1 || b.Env[0] != (KV{"B", "two"}) ||
		len(b.Outputs) != 1 || b.Outputs[0] != (KV{"out", "${{ steps.s.outputs.x }}"}) || b.Strategy == nil || len(b.Steps) != 3 {
		t.Errorf("job = %+v", b)
	}
	if got := wf.Jobs[1].Needs; len(got) != 1 || got[0] != "test" {
		t.Errorf("a scalar needs = %v", got)
	}
	s := b.Steps[0]
	if s.ID != "s" || s.Name != "Named" || s.If != "success()" || s.WorkDir != "sub" || s.Shell != "sh" ||
		s.ContinueOnError != "true" || s.Run != "echo hi" || len(s.Env) != 1 || s.Env[0] != (KV{"C", "three"}) {
		t.Errorf("step = %+v", s)
	}
	if b.Steps[1].Uses != "actions/checkout@v4" || b.Steps[1].Run != "" {
		t.Errorf("uses step = %+v", b.Steps[1])
	}
}

func TestStep_LabelNamesWhatTheStepIs(t *testing.T) {
	for _, c := range []struct {
		s    Step
		want string
	}{
		{Step{Name: "Named", Run: "x"}, "Named"},
		{Step{Uses: "actions/checkout@v4"}, "actions/checkout@v4"},
		{Step{Run: "go test ./...\nsecond"}, "go test ./..."},
		{Step{Line: 12}, "step at line 12"},
	} {
		if got := c.s.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestParse_RefusesAMalformedWorkflowNamingWhy(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"list at top", "- a\n- b\n", "map at the top"},
		{"no jobs", "on: pull_request\n", "no jobs"},
		{"job not a map", "on: pull_request\njobs:\n  j: x\n", "a job is a map"},
		{"no steps", "on: pull_request\njobs:\n  j:\n    name: x\n", "no steps"},
		{"step needs run or uses", "on: pull_request\njobs:\n  j:\n    steps:\n      - name: x\n", "run or uses"},
		{"step with both", "on: pull_request\njobs:\n  j:\n    steps:\n      - run: a\n        uses: b\n", "not both"},
		{"step not a map", "on: pull_request\njobs:\n  j:\n    steps:\n      - echo\n", "a step is a map"},
		{"bad timeout", "on: pull_request\njobs:\n  j:\n    timeout-minutes: soon\n    steps:\n      - run: a\n", "timeout-minutes"},
		{"env expression", "on: pull_request\nenv: ${{ x }}\njobs:\n  j:\n    steps:\n      - run: a\n", "env as an expression"},
		{"env list", "on: pull_request\nenv: [a]\njobs:\n  j:\n    steps:\n      - run: a\n", "env must be a map"},
		{"env nested", "on: pull_request\nenv:\n  A:\n    b: 1\njobs:\n  j:\n    steps:\n      - run: a\n", "must be a scalar"},
		{"needs map", "on: pull_request\njobs:\n  j:\n    needs:\n      a: 1\n    steps:\n      - run: a\n", "needs is a job id"},
		{"step env bad", "on: pull_request\njobs:\n  j:\n    steps:\n      - run: a\n        env: [x]\n", "env"},
		{"yaml error", "on: [", "line 1"},
	} {
		_, _, err := Parse("w.yml", c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

func writeFlow(t *testing.T, repo, name, body string) {
	t.Helper()
	dir := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDir_KeepsPullRequestWorkflowsInNameOrderAndNamesTheRest(t *testing.T) {
	repo := t.TempDir()
	writeFlow(t, repo, "b.yml", "on: pull_request\njobs:\n  j:\n    steps:\n      - run: a\n")
	writeFlow(t, repo, "a.yaml", "on: [pull_request]\njobs:\n  j:\n    steps:\n      - run: a\n")
	writeFlow(t, repo, "nightly.yml", "on:\n  schedule:\n    - cron: '0 0 * * *'\njobs:\n  j:\n    steps:\n      - run: a\n")
	writeFlow(t, repo, "weird.yml", "on: push\nx: &anchor 1\njobs:\n  j:\n    steps:\n      - run: a\n")
	flows, skipped, err := LoadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range flows {
		names = append(names, f.File)
	}
	if got := strings.Join(names, ","); got != "a.yaml,b.yml" {
		t.Fatalf("flows = %s, want them in file-name order", got)
	}
	joined := strings.Join(skipped, "\n")
	if !strings.Contains(joined, "nightly.yml: does not run on pull_request") {
		t.Errorf("the nightly workflow must be named as skipped:\n%s", joined)
	}
	if !strings.Contains(joined, "weird.yml: unreadable") || !strings.Contains(joined, "never mentions pull_request") {
		t.Errorf("an unreadable workflow that never mentions pull_request is set aside by name:\n%s", joined)
	}
}

func TestLoadDir_AnUnreadableWorkflowThatMentionsPullRequestIsRefused(t *testing.T) {
	repo := t.TempDir()
	writeFlow(t, repo, "bad.yml", "on: pull_request\nx: &anchor 1\njobs:\n  j:\n    steps:\n      - run: a\n")
	_, _, err := LoadDir(repo)
	if err == nil || !strings.Contains(err.Error(), "bad.yml") {
		t.Fatalf("err = %v, want a refusal naming bad.yml", err)
	}
}

func TestLoadDir_NoWorkflowsIsAnEmptyResultNotAnError(t *testing.T) {
	flows, skipped, err := LoadDir(t.TempDir())
	if err != nil || len(flows) != 0 || len(skipped) != 0 {
		t.Errorf("got %v, %v, %v", flows, skipped, err)
	}
}
