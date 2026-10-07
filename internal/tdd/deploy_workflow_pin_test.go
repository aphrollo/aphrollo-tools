package tdd

import (
	"strings"
	"testing"
)

// There is no deploy-on-merge: a box runs the binary `aphrollo update` installs
// into its user space, so the pipeline tags a release and ships nothing.

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

func TestPipeline_ReleaseJobShipsNothingAndThereIsNoDeployWorkflow(t *testing.T) {
	t.Parallel()
	rel := jobNamed(t, "pipeline.yml", "release").text
	for _, banned := range []string{"gh workflow run", "actions: write", "deploy.yml"} {
		if strings.Contains(rel, banned) {
			t.Errorf("release job carries %q: it tags, and tagging is all it does", banned)
		}
	}
	if _, found := workflowFiles(t)["deploy.yml"]; found {
		t.Error("deploy.yml exists: deploy-on-merge was removed; boxes update themselves with aphrollo update")
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

// The release job pushes a real version tag, and `aphrollo update` follows the
// newest tag. A manual dispatch from a lane branch would find that branch's pending
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

// ratchet: test_removed TestPipeline_ReleaseDispatchesTheDeployWorkflowAfterTagging: deploy-on-merge was removed; TestPipeline_ReleaseJobShipsNothingAndThereIsNoDeployWorkflow pins the opposite
// ratchet: test_removed TestDeployWorkflow_IsDispatchOnlyAndSupersedable: deploy.yml is gone with the deploy; the same test pins that it stays gone
// ratchet: test_removed TestBinaryBehindLine_WhenInstallIsNotWritable_NamesTheDeployPipelineInstead: an update installs into the user's own space, so the behind line always says run aphrollo update
// ratchet: test_removed deploy/deploy_installer_test.go: deploy/deploy-prod.sh was removed with deploy-on-merge, and the test file with it
// ratchet: test_removed internal/cli/deploystamp_workflow_test.go: it pinned deploy.yml's go build stamp keys; deploy.yml is gone and aphrollo update stamps through buildArgs
