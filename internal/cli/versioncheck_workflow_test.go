package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// pipelineJob is one top-level job of this repo's pipeline.yml, bounded by the
// next top-level job key so a neighbouring job's text can never satisfy a pin.
func pipelineJob(t *testing.T, name string) string {
	t.Helper()
	root := tdd.RepoRoot(".")
	if root == "" {
		t.Fatal("this test reads this repo's own pipeline.yml and could not find its root")
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "pipeline.yml"))
	if err != nil {
		t.Fatalf("read pipeline.yml: %v", err)
	}
	jobKey := regexp.MustCompile(`^  [A-Za-z][A-Za-z0-9_-]*:\s*$`)
	var job []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if jobKey.MatchString(line) {
			in = line == "  "+name+":"
			continue
		}
		if in {
			job = append(job, line)
		}
	}
	if len(job) == 0 {
		t.Fatalf("no %q job in pipeline.yml, so the version rule is enforced nowhere", name)
	}
	return strings.Join(job, "\n")
}

// The version rule lives in `aphrollo version check`; the pipeline is where it
// is held against every pull request. A job that is skipped for a PR, reads a
// stale body, swallows its failure or measures from the wrong base is a rule
// that does not bind, so each of those is pinned.
func TestPipeline_VersionCheckHoldsEveryPullRequestToTheVersionRule(t *testing.T) {
	t.Parallel()
	job := pipelineJob(t, "version-check")

	for _, want := range []struct{ text, why string }{
		{"github.event_name == 'pull_request'", "a push to main has no PR body to read"},
		{"github.event.pull_request.draft != true", "draft PRs run no CI, like every other job"},
		{"github.actor != 'dependabot[bot]'", "a bot's dependency bump cannot write the line, and moves no verdict"},
		{"fetch-depth: 0", "the base commit must be in the clone to read its VERSION and diff against it"},
		{"github.event.pull_request.base.sha", "the change is measured from the PR's own base"},
		{"ref: ${{ github.event.pull_request.head.sha }}", "HEAD must be the PR's own head, not the merge ref, so the check measures the branch from its merge base with the base and never a base that moved on"},
		{"gh pr view", "the body is read live, so a re-run after editing the body sees the edit, not the event's stale copy"},
		{"pull-requests: read", "reading the body needs that scope and nothing wider"},
		{"version check", "the job must run the check"},
		{"--base", "the check needs the base to measure from"},
		{"--body-file", "the check needs the body"},
	} {
		if !strings.Contains(job, want.text) {
			t.Errorf("the version-check job lacks %q: %s", want.text, want.why)
		}
	}
	for _, forbidden := range []string{"continue-on-error", "|| true", ": write"} {
		if strings.Contains(job, forbidden) {
			t.Errorf("the version-check job contains %q, which would let a refusal pass or widen its token", forbidden)
		}
	}
}
