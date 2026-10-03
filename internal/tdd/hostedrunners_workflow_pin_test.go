package tdd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This repo is public: a pull_request job on the org's self-hosted runners
// would run a fork's code on the production host as the user that deploys.
// These tests pin the runner policy in every workflow file: a job that can
// see a pull_request event runs on a GitHub-hosted runner, a self-hosted job
// carries an `if:` that only a push, a schedule or a dispatch satisfies, no
// pull_request_target workflow schedules onto the host, and every workflow and
// job declares its own permissions.

type workflowJob struct {
	file, name, text string
}

var (
	jobHeaderRe   = regexp.MustCompile(`^  ([A-Za-z][A-Za-z0-9_-]*):\s*$`)
	runsOnLineRe  = regexp.MustCompile(`(?m)^    runs-on:\s*(.+?)\s*$`)
	jobIfLineRe   = regexp.MustCompile(`(?m)^    if:\s*(.+?)\s*$`)
	jobPermsRe    = regexp.MustCompile(`(?m)^    permissions:`)
	topPermsRe    = regexp.MustCompile(`(?m)^permissions:`)
	matrixRunnerR = regexp.MustCompile(`(?m)^\s+runner:\s*(\S+)\s*$`)
)

func workflowFiles(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(repoRootForTest(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = stripYAMLComments(string(data))
	}
	if len(out) == 0 {
		t.Fatal("no workflow files found, so these tests prove nothing")
	}
	return out
}

// stripYAMLComments drops whole-line comments so prose that mentions a runner
// or an event is never read as configuration.
func stripYAMLComments(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

func workflowJobs(file, text string) []workflowJob {
	lines := strings.Split(text, "\n")
	var jobs []workflowJob
	inJobs := false
	start, name := -1, ""
	flush := func(end int) {
		if start >= 0 {
			jobs = append(jobs, workflowJob{file, name, strings.Join(lines[start:end], "\n")})
		}
	}
	for i, line := range lines {
		if !inJobs {
			inJobs = line == "jobs:"
			continue
		}
		if m := jobHeaderRe.FindStringSubmatch(line); m != nil {
			flush(i)
			start, name = i, m[1]
		}
	}
	flush(len(lines))
	return jobs
}

func TestPipeline_SelfHostedJobsAreGuardedAgainstPullRequests(t *testing.T) {
	t.Parallel()
	guard := regexp.MustCompile(`github\.event_name == '(push|schedule|workflow_dispatch)'`)
	for file, text := range workflowFiles(t) {
		jobs := workflowJobs(file, text)
		if len(jobs) == 0 {
			t.Fatalf("%s: no jobs parsed", file)
		}
		for _, j := range jobs {
			runsOn := ""
			if m := runsOnLineRe.FindStringSubmatch(j.text); m != nil {
				runsOn = m[1]
			}
			selfHosted := strings.Contains(runsOn, "self-hosted")
			if m := matrixRunnerR.FindAllStringSubmatch(j.text, -1); strings.Contains(runsOn, "matrix.") {
				for _, r := range m {
					selfHosted = selfHosted || strings.Contains(r[1], "self-hosted")
				}
			}
			if !selfHosted {
				continue
			}
			ifm := jobIfLineRe.FindStringSubmatch(j.text)
			if ifm == nil {
				t.Errorf("%s job %q runs on self-hosted with no `if:` guard", file, j.name)
				continue
			}
			cond := ifm[1]
			if !guard.MatchString(cond) || strings.Contains(cond, "pull_request") {
				t.Errorf("%s job %q runs on self-hosted; its `if:` must admit only push, schedule or workflow_dispatch and never mention pull_request: %s", file, j.name, cond)
			}
		}
		if strings.Contains(text, "pull_request_target") && strings.Contains(text, "self-hosted") {
			t.Errorf("%s: a pull_request_target workflow must never use a self-hosted runner", file)
		}
	}
}

func TestPipeline_EveryWorkflowAndJobDeclaresPermissions(t *testing.T) {
	t.Parallel()
	for file, text := range workflowFiles(t) {
		if !topPermsRe.MatchString(text) {
			t.Errorf("%s has no top-level `permissions:`", file)
		}
		for _, j := range workflowJobs(file, text) {
			if !jobPermsRe.MatchString(j.text) {
				t.Errorf("%s job %q has no job-level `permissions:`", file, j.name)
			}
		}
	}
}

// ratchet: test_removed TestPipeline_OnlyDeployStaysSelfHostedInThePipeline: deploy left the pipeline; NoJobInThePipelineIsSelfHosted pins the stricter rule
func TestPipeline_NoJobInThePipelineIsSelfHosted(t *testing.T) {
	t.Parallel()
	text := workflowFiles(t)["pipeline.yml"]
	for _, j := range workflowJobs("pipeline.yml", text) {
		if strings.Contains(j.text, "self-hosted") {
			t.Errorf("pipeline.yml job %q must run on a GitHub-hosted runner; the host-touching deploy lives in deploy.yml", j.name)
		}
	}
}
