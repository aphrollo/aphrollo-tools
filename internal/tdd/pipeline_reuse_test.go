package tdd

import (
	"regexp"
	"strings"
	"testing"
)

// A push to main re-ran the whole suite on a commit whose tree the merged
// pull request's own run had already tested green: twice the load for no new
// information. The `changes` job now asks `cireuse` whether that is the case
// and the heavy jobs stand down on `reuse`. Everything below pins the parts of
// that wiring a workflow edit can silently break.

// jobIfLine is the job-level `if:` of a job block: the first `    if:` line.
func jobIfLine(t *testing.T, job string) string {
	t.Helper()
	for _, line := range strings.Split(job, "\n") {
		if strings.HasPrefix(line, "    if:") {
			return line
		}
	}
	t.Fatalf("job has no job-level if:\n%s", job)
	return ""
}

const reuseGuard = "needs.changes.outputs.reuse != 'true'"

var heavyJobs = []string{"test", "test-windows", "gate-env", "lint", "benchmarks"}

func TestPipeline_HeavyJobsStandDownWhenAPullRequestAlreadyTestedTheTree(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	for _, name := range heavyJobs {
		line := jobIfLine(t, pipelineJobBlock(t, wf, name))
		if !strings.Contains(line, reuseGuard) {
			t.Errorf("job %q does not stand down on reuse: %s", name, line)
		}
		if !strings.Contains(line, "github.event.pull_request.draft != true") {
			t.Errorf("job %q lost its draft guard: %s", name, line)
		}
	}
}

// What is not tree-deterministic, or is the verdict itself, keeps running on a
// push: scan (govulncheck reads a vulnerability database that moves without
// the tree), the doc and workflow checks, the merged-tree ratchet, release.
func TestPipeline_OnlyTheTreeDeterministicJobsStandDown(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	for _, name := range []string{"scan", "docs-check", "workflow-pins", "merged-tree-ratchet", "release", "changes"} {
		block := pipelineJobBlock(t, wf, name)
		if strings.Contains(jobIfLine(t, block), "outputs.reuse") {
			t.Errorf("job %q stands down on reuse, but it judges something the tree alone does not settle", name)
		}
	}
}

// release tags the merge and deploy follows it; a skipped test job must not
// hold either back, and a failed one still must.
func TestPipeline_ReleaseRunsWhenReuseSkippedTheTests(t *testing.T) {
	t.Parallel()
	block := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "release")
	line := jobIfLine(t, block)
	if !strings.Contains(line, "always()") {
		t.Errorf("release has no always(), so a skipped test job skips the release too: %s", line)
	}
	if !strings.Contains(line, "!contains(needs.*.result, 'failure')") || !strings.Contains(line, "!contains(needs.*.result, 'cancelled')") {
		t.Errorf("release no longer refuses a failed or cancelled need: %s", line)
	}
	if strings.Contains(line, "needs.test.result == 'success'") {
		t.Errorf("release demands a green test job, which reuse leaves skipped: %s", line)
	}
}

func TestPipeline_ChangesJobDecidesReuseOnAPushOnly(t *testing.T) {
	t.Parallel()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "changes")
	for _, want := range []string{
		"reuse: ${{ steps.reuse.outputs.reuse }}",
		"actions: read",
		"pull-requests: read",
		"go build -trimpath -buildvcs=false -o bin/cireuse ./tools/cireuse",
		"GH_TOKEN: ${{ github.token }}",
		"./bin/cireuse",
		"-tree \"$(git rev-parse HEAD^{tree})\"",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("changes job lacks %q", want)
		}
	}
	i := strings.Index(job, "id: reuse")
	if i < 0 {
		t.Fatal("changes job has no `id: reuse` step")
	}
	step := job[strings.LastIndex(job[:i], "      - "):]
	if next := strings.Index(step[1:], "\n      - "); next >= 0 {
		step = step[:next+1]
	}
	if !strings.Contains(step, "if: github.event_name == 'push'") {
		t.Errorf("the reuse step runs on events other than a push:\n%s", step)
	}
	if !strings.Contains(step, "steps.filter.outputs.class") || !strings.Contains(step, "code") {
		t.Errorf("the reuse step does not require the code class, so a docs-only push would change behaviour:\n%s", step)
	}
	if !strings.Contains(step, "reuse=false") {
		t.Errorf("a cireuse that fails or was never built must leave reuse=false:\n%s", step)
	}
	if !strings.Contains(step, "GITHUB_STEP_SUMMARY") {
		t.Errorf("the reuse step does not say in the job summary whose verdict was reused:\n%s", step)
	}
}

// The build of cireuse is best effort like the classifier's: a tree that does
// not compile must not fail the changes job, only leave reuse off.
func TestPipeline_ReuseToolBuildFailureLeavesTheFullRunOn(t *testing.T) {
	t.Parallel()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "changes")
	i := strings.Index(job, "-o bin/cireuse ./tools/cireuse")
	if i < 0 {
		t.Fatal("no cireuse build in the changes job, so this test proves nothing")
	}
	step := job[:i]
	step = step[strings.LastIndex(step, "      - "):]
	if !strings.Contains(step, "continue-on-error: true") {
		t.Errorf("the cireuse build is not continue-on-error:\n%s", step)
	}
}

// The checks the tool demands of the pull request's run are named in the
// workflow, next to the jobs they judge. Each must name a real job with a
// step of that name, and every job that stands down on reuse (benchmarks
// aside, which only has work when its filter says so) must be demanded, or
// the push would skip a job whose pull-request run nobody looked at.
func TestPipeline_ReuseRequirementsNameRealJobsAndStepsAndCoverTheSkippedJobs(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	job := pipelineJobBlock(t, wf, "changes")
	reqRe := regexp.MustCompile(`-require '([^=']+)=([^']+)'`)
	required := map[string]bool{}
	for _, m := range reqRe.FindAllStringSubmatch(job, -1) {
		name, prefix := m[1], m[2]
		required[name] = true
		block := pipelineJobBlock(t, wf, name)
		if !regexp.MustCompile(`(?m)^      - (?:if: [^\n]*\n        )?name: ` + regexp.QuoteMeta(prefix)).MatchString(block) {
			t.Errorf("-require %s=%s: job %s has no step named %q...", name, prefix, name, prefix)
		}
	}
	for _, name := range heavyJobs {
		if name != "benchmarks" && !required[name] {
			t.Errorf("job %q stands down on reuse but the pull request's run is not required to have passed it", name)
		}
	}
	if len(required) == 0 {
		t.Fatal("no -require in the changes job, so this test proves nothing")
	}
}

// The tool reads the tree a run tested from an artifact that run uploaded, so
// the upload is a step of `changes` for a pull request and for a merge group,
// which check out the same commit every job of the run does, and for no push.
func TestPipeline_PullRequestRunPublishesTheTreeItTested(t *testing.T) {
	t.Parallel()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "changes")
	for _, want := range []string{
		"git rev-parse HEAD^{tree} > tested-tree/tree",
		"name: tested-tree",
		"path: tested-tree/tree",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("changes job lacks %q", want)
		}
	}
	i := strings.Index(job, "name: tested-tree")
	step := job[:i]
	step = step[strings.LastIndex(step, "      - "):]
	const cond = "if: github.event_name == 'pull_request' || github.event_name == 'merge_group'\n"
	if !strings.Contains(step, "uses: actions/upload-artifact@") || !strings.Contains(step, cond) {
		t.Errorf("the upload is not a pull_request and merge_group upload-artifact step:\n%s", step)
	}
	j := strings.Index(job, "git rev-parse HEAD^{tree} > tested-tree/tree")
	record := job[:j]
	record = record[strings.LastIndex(record, "      - "):]
	if !strings.Contains(record, cond) {
		t.Errorf("the tree is not recorded on a merge_group run, so the queue's verdict has no tree to match:\n%s", record)
	}
}
