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

func TestOptions_WithDefaultsFillsOnlyWhatIsUnset(t *testing.T) {
	d := Options{}.withDefaults()
	if d.StepTimeout != 30*time.Minute {
		t.Errorf("default step timeout = %v, want 30m", d.StepTimeout)
	}
	if d.Out == nil || len(d.Env) == 0 {
		t.Errorf("defaults = %+v, want a writer and the process environment", d)
	}
	if neg := (Options{StepTimeout: -1}).withDefaults(); neg.StepTimeout != 30*time.Minute {
		t.Errorf("a negative timeout must take the default, got %v", neg.StepTimeout)
	}
	var sink bytes.Buffer
	kept := Options{StepTimeout: time.Second, Out: &sink, Env: []string{}}.withDefaults()
	if kept.StepTimeout != time.Second || kept.Out != &sink || len(kept.Env) != 0 {
		t.Errorf("explicit options were overwritten: %+v", kept)
	}
	if waitDelay != 10*time.Second {
		t.Errorf("waitDelay = %v, want 10s", waitDelay)
	}
}

func TestRun_JobLinesCarryTheirDetailOnlyWhenThereIsOne(t *testing.T) {
	_, out, _ := runFlow(t, `
on: pull_request
jobs:
  fine:
    steps:
      - run: echo ok
  broken:
    steps:
      - run: exit 3
`)
	if !strings.Contains(out, "ci run: job fine: success\n") {
		t.Errorf("a clean job line must have no parentheses:\n%s", out)
	}
	if !strings.Contains(out, "ci run: job broken (step failed: exit 3): failure\n") {
		t.Errorf("a failed job line must carry its detail:\n%s", out)
	}
}

func TestRun_AJobTimeoutLeavesAFastJobAlone(t *testing.T) {
	sum, out, _ := runFlow(t, "on: pull_request\njobs:\n  j:\n    timeout-minutes: 5\n    steps:\n      - run: echo ok\n")
	if r := result(t, sum, "j"); r.Result != ResultSuccess {
		t.Errorf("a job well inside its timeout failed: %+v\n%s", r, out)
	}
}

func TestRun_AnEmptyIncludeSkipsTheJob(t *testing.T) {
	sum, _, _ := runFlow(t, "on: pull_request\njobs:\n  j:\n    strategy:\n      matrix:\n        include: []\n    steps:\n      - run: echo never\n")
	if r := result(t, sum, "j"); r.Result != ResultSkipped {
		t.Errorf("j = %+v, want skipped for an empty matrix", r)
	}
}

func TestParseCommandFile_ReadsEdgeShapes(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     []KV
	}{
		{"block with an empty key", "<<D\nbody\nD\n", []KV{{"", "body"}}},
		{"an equals sign first hides the block", "=a<<D\nx\nD\nok=1\n", []KV{{"ok", "1"}}},
		{"an unclosed block runs to the end", "k<<D\nl1\nl2\n", []KV{{"k", "l1\nl2\n"}}},
		{"an unclosed block at the very end", "k<<D", []KV{{"k", ""}}},
		{"an empty delimiter", "k<<\nbody\n\nnext=1\n", []KV{{"k", "body"}, {"next", "1"}}},
		{"equals before the block marker", "k=a<<b\n", []KV{{"k", "a<<b"}}},
		{"a bare name is not an entry", "name\n", nil},
	} {
		got := parseCommandFile(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: entry %d = %v, want %v", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestFileExists_IsTrueForFilesOnly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(file) {
		t.Error("an existing file must exist")
	}
	if fileExists(dir) {
		t.Error("a directory is not a file")
	}
	if fileExists(filepath.Join(dir, "missing")) {
		t.Error("a missing path is not a file")
	}
}

func TestRun_AStepThatHasNoShellFailsItsJobNotTheProcess(t *testing.T) {
	fakeBash(t, "", os.ErrNotExist)
	wf, _, err := Parse("ci.yml", "on: pull_request\njobs:\n  j:\n    steps:\n      - run: echo hi\n")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sum, err := Run(context.Background(), []*Workflow{wf}, Options{Dir: t.TempDir(), Out: &out})
	if err != nil || !sum.Failed() {
		t.Fatalf("a missing bash must fail the job: %v %+v\n%s", err, sum, out.String())
	}
}
