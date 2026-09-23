//go:build !windows

package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The filter step is a bash script run on the Linux runner, so this file
// runs it where bash is the system shell and stays out of a Windows build.
// The workflow-pins job runs it too: its name carries the TestPipeline_
// prefix that job selects.

// filterScript returns the `run:` body of the changes job's `id: filter`
// step, dedented.
func filterScript(t *testing.T) string {
	t.Helper()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "changes")
	inStep, inRun := false, false
	indent := ""
	var body []string
	for _, line := range strings.Split(job, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inStep:
			inStep = trimmed == "- id: filter"
		case !inRun:
			inRun = trimmed == "run: |"
		case trimmed == "":
			body = append(body, "")
		default:
			if indent == "" {
				indent = line[:len(line)-len(strings.TrimLeft(line, " "))]
			}
			if !strings.HasPrefix(line, indent) {
				return strings.Join(body, "\n")
			}
			body = append(body, strings.TrimPrefix(line, indent))
		}
	}
	if len(body) == 0 {
		t.Fatal("no `id: filter` step with a `run: |` body in the changes job, so this test proves nothing")
	}
	return strings.Join(body, "\n")
}

// runFilter runs the filter step's own script in a repository whose only
// change touches internal/tdd, with bin/aphrollo standing in for the
// classifier ("" = no binary at all), and returns what the script wrote to
// GITHUB_OUTPUT.
func runFilter(t *testing.T, script, classifier string) map[string]string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is the filter step's shell and is not on PATH: %v", err)
	}
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "README.md", "a\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "base")
	base := strings.TrimSpace(gitOut(root, "rev-parse", "HEAD"))
	write(t, root, "internal/tdd/x.go", "package tdd\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "change")
	if classifier != "" {
		write(t, root, "bin/aphrollo", "#!/bin/sh\n"+classifier+"\n")
		if err := os.Chmod(filepath.Join(root, "bin", "aphrollo"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outFile := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.Command(bash, "-c", script)
	cmd.Dir = root
	cmd.Env = append(cleanGitEnv(), "BASE_SHA="+base, "GITHUB_OUTPUT="+outFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the filter step failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("the filter step wrote no GITHUB_OUTPUT: %v", err)
	}
	got := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

// The filter step itself, run for real: each class sets the outputs its
// jobs read, and a classifier that fails, prints something unknown, or was
// never built leaves the full run on.
func TestPipeline_FilterStepMapsEachClassAndFailsSafeToCode(t *testing.T) {
	script := filterScript(t)
	cases := []struct {
		name, classifier string
		want             map[string]string
	}{
		{"docs-only", "echo docs-only", map[string]string{"class": "docs-only", "code": "false", "lint": "false", "workflow": "false", "bench": "false", "refactor": "false"}},
		{"comment-only", "echo comment-only", map[string]string{"class": "comment-only", "code": "false", "lint": "true", "workflow": "false", "bench": "false"}},
		{"workflow-only", "echo workflow-only", map[string]string{"class": "workflow-only", "code": "false", "lint": "false", "workflow": "true", "bench": "false"}},
		{"code", "echo code", map[string]string{"class": "code", "code": "true", "lint": "true", "workflow": "false", "bench": "true"}},
		{"classifier fails", "echo docs-only; exit 1", map[string]string{"class": "code", "code": "true", "lint": "true", "workflow": "false"}},
		{"unknown class", "echo fast", map[string]string{"class": "code", "code": "true", "lint": "true"}},
		{"no classifier built", "", map[string]string{"class": "code", "code": "true", "lint": "true"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runFilter(t, script, c.classifier)
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("output %s = %q, want %q (all outputs: %v)", k, got[k], v, got)
				}
			}
		})
	}
}
