package tdd

import (
	"strings"
	"testing"
)

// The release tag must never wait on the deploy host. `deploy` waits for the
// one self-hosted runner; inside the pipeline run it held the run open, every
// later push-to-main run queued behind it, and GitHub cancelled all but the
// newest, so their `release` jobs never ran. The deploy lives in its own
// workflow, dispatched by the release job once the tag step is done.

func jobNamed(t *testing.T, file, job string) workflowJob {
	t.Helper()
	for _, j := range workflowJobs(file, workflowFiles(t)[file]) {
		if j.name == job {
			return j
		}
	}
	t.Fatalf("%s has no job %q", file, job)
	return workflowJob{}
}

func TestPipeline_HasNoDeployJob(t *testing.T) {
	t.Parallel()
	for _, j := range workflowJobs("pipeline.yml", workflowFiles(t)["pipeline.yml"]) {
		if j.name == "deploy" {
			t.Error("pipeline.yml must not carry the deploy job: waiting on the self-hosted runner blocks later releases")
		}
	}
}

func TestPipeline_ReleaseDispatchesTheDeployWorkflowAfterTagging(t *testing.T) {
	t.Parallel()
	rel := jobNamed(t, "pipeline.yml", "release").text
	if !strings.Contains(rel, "gh workflow run deploy.yml") {
		t.Error("release job must dispatch deploy.yml with `gh workflow run deploy.yml`")
	}
	if !strings.Contains(rel, "actions: write") {
		t.Error("release job needs `actions: write` to dispatch deploy.yml")
	}
	if strings.Index(rel, "tag-release.sh") > strings.Index(rel, "gh workflow run") {
		t.Error("release job must tag before it dispatches the deploy")
	}
}

func TestDeployWorkflow_IsDispatchOnlyAndSupersedable(t *testing.T) {
	t.Parallel()
	text := workflowFiles(t)["deploy.yml"]
	if text == "" {
		t.Fatal("no .github/workflows/deploy.yml")
	}
	head := text[:strings.Index(text, "\njobs:")]
	if !strings.Contains(head, "workflow_dispatch:") || strings.Contains(head, "pull_request") || strings.Contains(head, "push:") {
		t.Errorf("deploy.yml must trigger on workflow_dispatch only:\n%s", head)
	}
	if !strings.Contains(head, "group: deploy\n") || !strings.Contains(head, "cancel-in-progress: true") {
		t.Errorf("deploy.yml needs concurrency group `deploy` with cancel-in-progress: true:\n%s", head)
	}
	d := jobNamed(t, "deploy.yml", "deploy").text
	if !strings.Contains(d, "runs-on: self-hosted") {
		t.Error("deploy job runs on self-hosted")
	}
	if !strings.Contains(d, "github.ref == 'refs/heads/main'") {
		t.Error("deploy job must be guarded to main")
	}
	if !strings.Contains(d, "deploy/newest-tag.sh") || !strings.Contains(d, "./deploy/deploy-prod.sh") {
		t.Error("deploy job must check out the newest tag and run deploy-prod.sh")
	}
}

// A release tag is made from the changelog fragments, by the binary built from
// the very commit being released, and never by a bot committing to main. Two
// pushes in quick succession must queue behind each other, not race for the
// same tag, and not cancel a tag in flight.
func TestPipeline_ReleaseJobTagsFromTheFragmentsOfTheCommitItBuilds(t *testing.T) {
	t.Parallel()
	rel := jobNamed(t, "pipeline.yml", "release").text
	build, tag := strings.Index(rel, "go build"), strings.Index(rel, "tag-release.sh")
	if build < 0 || tag < 0 || build > tag {
		t.Error("release job must build aphrollo from this checkout before tag-release.sh, which asks it for the plan")
	}
	for _, want := range []struct{ text, why string }{
		{"fetch-depth: 0", "the plan reads every tag and the history behind it"},
		{"group: release-tag-aphrollo-cli", "two pushes in quick succession serialize on one group"},
		{"cancel-in-progress: false", "a tag in flight is never cancelled by the next push"},
		{"contents: write", "pushing the tag and creating the Release need it"},
		{"GH_TOKEN: ${{ github.token }}", "tag-release.sh calls gh for the Release"},
	} {
		if !strings.Contains(rel, want.text) {
			t.Errorf("release job lacks %q: %s", want.text, want.why)
		}
	}
	if strings.Contains(rel, "git commit") || strings.Contains(rel, "git push origin main") {
		t.Error("release job must not commit to main: a version is a tag, not a commit")
	}
}

// The release job pushes a real version tag, and deploy.yml ships the newest
// tag. A manual dispatch from a lane branch would find that branch's pending
// fragment and tag an unmerged commit, so both arms of the trigger are held to
// main.
func TestPipeline_ReleaseJobRunsOnlyOnMainWhateverTheTrigger(t *testing.T) {
	t.Parallel()
	rel := jobNamed(t, "pipeline.yml", "release").text
	want := "((github.event_name == 'push' || github.event_name == 'workflow_dispatch') && github.ref == 'refs/heads/main')"
	if !strings.Contains(rel, want) {
		t.Errorf("the release job's `if` must hold push and workflow_dispatch alike to main, as %q:\n%s", want, rel)
	}
}
