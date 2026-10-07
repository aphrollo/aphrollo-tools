package tdd

import (
	"regexp"
	"strings"
	"testing"
)

// requiredChecks are the contexts main's branch protection requires. GitHub's
// merge queue waits for each of them on the queue's own `merge_group` run, so a
// job that does not report there leaves every queued pull request pending
// until the queue times it out.
var requiredChecks = []string{"test", "lint", "build", "docs-check", "workflow-pins", "pr-ratchet"}

// onBlock is the text of the workflow's top-level `on:` map.
func onBlock(t *testing.T, wf string) string {
	t.Helper()
	i := strings.Index(wf, "\non:\n")
	j := strings.Index(wf, "\npermissions:\n")
	if i < 0 || j < i {
		t.Fatal("pipeline.yml has no `on:` block before `permissions:`, so this test proves nothing")
	}
	return wf[i:j]
}

func TestPipeline_RunsOnTheMergeQueuesCheckRequest(t *testing.T) {
	t.Parallel()
	on := onBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"))
	if !regexp.MustCompile(`(?m)^  merge_group:\n    types: \[checks_requested\]\n`).MatchString(on) {
		t.Errorf("the on: block has no `merge_group:` trigger with `types: [checks_requested]`, so a queued pull request gets no CI:\n%s", on)
	}
}

// needsOf lists the jobs a job waits for, read from its inline `needs:` line.
func needsOf(job string) []string {
	m := regexp.MustCompile(`(?m)^    needs: \[?([^\]\n]+)\]?$`).FindStringSubmatch(job)
	if m == nil {
		return nil
	}
	var out []string
	for _, n := range strings.Split(m[1], ",") {
		out = append(out, strings.TrimSpace(n))
	}
	return out
}

// A required check that is skipped counts as passing, and one that never
// starts is pending forever; both are silent. So every required job, and every
// job it needs, must not name an event in its `if:` without naming merge_group:
// the draft guard alone is true there, because the field is absent.
func TestPipeline_EveryRequiredCheckCanRunOnAMergeGroup(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	seen := map[string]bool{}
	var walk func(name, via string)
	walk = func(name, via string) {
		if seen[name] {
			return
		}
		seen[name] = true
		job := pipelineJobBlock(t, wf, name)
		line := jobIfLine(t, job)
		if strings.Contains(line, "github.event_name") && !strings.Contains(line, "merge_group") {
			t.Errorf("job %q (%s) gates on the event without naming merge_group, so it skips on a queue run: %s", name, via, line)
		}
		for _, n := range needsOf(job) {
			walk(n, "needed by "+name)
		}
	}
	for _, name := range requiredChecks {
		walk(name, "required")
	}
}

// pr-ratchet is required and cheap, so it runs on a merge group rather than
// skipping. A queue entry is not a merge of two parents, so the base it scopes
// the diff-scoped laws to is the group's own base_sha.
func TestPipeline_RatchetRunsOnAMergeGroupScopedToItsBase(t *testing.T) {
	t.Parallel()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "pr-ratchet")
	if !strings.Contains(jobIfLine(t, job), "github.event_name == 'merge_group'") {
		t.Error("pr-ratchet does not run on merge_group")
	}
	if !strings.Contains(job, "github.event.merge_group.base_sha") {
		t.Error("pr-ratchet does not read github.event.merge_group.base_sha, so it would take HEAD^2 of a commit that has none")
	}
}

// Every diff base falls back through the merge group's base_sha; without it a
// queue run diffs against an empty string and the classifier answers code for
// every change, or the docs check finds no base.
func TestPipeline_EveryDiffBaseNamesTheMergeGroupBase(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	sites := 0
	for _, line := range strings.Split(wf, "\n") {
		if !strings.Contains(line, "BASE_SHA:") {
			continue
		}
		sites++
		if !strings.Contains(line, "github.event.merge_group.base_sha") {
			t.Errorf("BASE_SHA line %q names no merge_group base", strings.TrimSpace(line))
		}
	}
	if sites == 0 {
		t.Fatal("no BASE_SHA site found in pipeline.yml, so this test proves nothing")
	}
}

// A queue run is never superseded by a newer one: its ref is unique, and a
// cancelled run reports nothing for the entry that is waiting on it.
func TestPipeline_NeverCancelsAMergeGroupRun(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	if !strings.Contains(wf, "cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n") {
		t.Error("cancel-in-progress is not limited to the pull_request event, so a merge_group run could be cancelled")
	}
}

// ratchet: test_removed TestPipeline_AMergeGroupRunTestsItsOwnTree: the owner decision of 2026-10-07 lets a merge group reuse its one pull request's green run of an equal tree; the replacement below pins that it does so only through the decision tool
// A queue run reuses another run's verdict only through `aphrollo ci reuse`,
// told it is a merge group: the tool then reuses the one pull request's run and
// only for an equal tree, and refuses a group of several pull requests. A reuse
// step that did not pass the event would judge a queue run as a push. The run
// publishes the tree it tested too (see
// TestPipeline_PullRequestRunPublishesTheTreeItTested), which is what lets the
// push that fast-forwards main to the group's head reuse it.
func TestPipeline_AMergeGroupRunReusesOnlyThroughTheDecisionToolToldItIsAMergeGroup(t *testing.T) {
	t.Parallel()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "changes")
	i := strings.Index(job, "id: reuse")
	if i < 0 {
		t.Fatal("changes job has no `id: reuse` step")
	}
	step := job[i:]
	if next := strings.Index(step, "\n      - "); next >= 0 {
		step = step[:next]
	}
	for _, want := range []string{
		"github.event_name == 'merge_group'",
		"./bin/aphrollo ci reuse",
		"-event \"$EVENT\"",
		"-head-ref \"$HEAD_REF\"",
		"-parent \"$PARENT\"",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("the reuse step lacks %q, so a merge_group run could be judged as a push or skip on a group's verdict the tool never saw", want)
		}
	}
}
