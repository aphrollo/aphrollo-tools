package tdd

import (
	"strings"
	"testing"
)

// prRatchetJobName is the pipeline.yml job this file pins: `aphrollo ratchet
// check` run over a PR's own merged tree, BEFORE the merge. Without it a law
// regression (doc_reference_exists, module_size, test_removed, ...) is
// invisible to CI: the local pre-commit/pre-merge gate is the only thing
// that runs the law engine on a lane, and this repo's auto-merge-on-green
// path lets a PR land without ever going through that gate.
// merged-tree-ratchet (below in the same file) catches the same regression,
// but only AFTER the merge already happened.
const prRatchetJobName = "pr-ratchet"

// prRatchetJobBlock isolates the job's own YAML text, the same top-level-key
// isolation gateEnvJobBlock and lintJobBlock use, kept as its own small copy
// (DAMP) so this file reads top to bottom without a jump to another test
// file's helper.
func prRatchetJobBlock(t *testing.T, workflow string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	start := -1
	for i, line := range lines {
		if start < 0 {
			if line == "  "+prRatchetJobName+":" {
				start = i
			}
			continue
		}
		if jobKeyLineRe.MatchString(line) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("no %q job in pipeline.yml, so this test proves nothing", prRatchetJobName)
	}
	return strings.Join(lines[start:], "\n")
}

// TestPipeline_RatchetRunsOnAPullRequestBeforeTheMerge pins the shape a
// pre-merge law check needs to actually close the gap it exists for:
// PR-scoped (a push to main has no merge ahead of it to check — that is
// merged-tree-ratchet's job, after the fact), skipped only for a docs-only
// PR (a code or comment change can regress a law; docs-check already covers
// the doc_reference_exists case for a docs-only PR, so this job would only
// duplicate it there), scoped with --base so the diff-scoped laws (e.g.
// test_removed) see what the tree used to carry, and never writing a
// baseline (--no-tighten).
func TestPipeline_RatchetRunsOnAPullRequestBeforeTheMerge(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	job := prRatchetJobBlock(t, wf)

	if !strings.Contains(job, "github.event_name == 'pull_request'") {
		t.Error("pr-ratchet does not gate on the pull_request event -- a push to main has no merge ahead of it for this job to check, that is merged-tree-ratchet's job")
	}
	if !strings.Contains(job, "needs.changes.outputs.code == 'true'") {
		t.Error("pr-ratchet does not skip on a docs-only PR (needs.changes.outputs.code) -- doc_reference_exists already covers markdown and docs-check already runs it there")
	}
	if !strings.Contains(job, "ratchet check") {
		t.Fatal("pr-ratchet does not invoke `ratchet check` at all")
	}
	if !strings.Contains(job, "--base") {
		t.Error("pr-ratchet does not pass --base -- the diff-scoped laws (test_removed) silently skip with no base to compare against")
	}
	if !strings.Contains(job, "--no-tighten") {
		t.Error("pr-ratchet does not pass --no-tighten -- a bare `ratchet check` here would tighten (write) a baseline on a one-shot PR checkout nobody commits")
	}
	if strings.Contains(job, "--adopt") {
		t.Error("pr-ratchet passes --adopt -- that writes or raises a baseline, which this job must never do")
	}
	if strings.Contains(job, "continue-on-error: true") {
		t.Error("pr-ratchet swallows its own failure with continue-on-error -- a law regression must fail the job")
	}
}
