package tdd

import (
	"regexp"
	"strings"
	"testing"
)

// lintJobName is the pipeline.yml job this file pins: both it and the local
// commit gate must invoke golangci-lint through the same box-wide,
// cross-account lint lock (lintlock.go) rather than colliding on
// golangci-lint's own — confirmed live on PR #740's self-hosted `lint` job
// (user github-runner) hitting the identical "parallel golangci-lint is
// running" a local commit gate (user debian) does, sharing the box's one
// /tmp.
const lintJobName = "lint"

// bareGolangciLintRunRe matches a DIRECT `golangci-lint run` invocation — the
// shape that bypasses this repo's own lint lock entirely. `./bin/aphrollo
// gate lint run ...` does not match: there is no "golangci-lint run"
// substring in "aphrollo gate lint run" (the verb right after "lint" there
// is `gate lint`'s own subcommand, not golangci-lint's binary name).
var bareGolangciLintRunRe = regexp.MustCompile(`(^|\s)golangci-lint run\b`)

// TestLintWorkflowStep_RunsGolangciLintThroughGateLintWrapper pins
// pipeline.yml's `lint` job to the ONE entry point (`aphrollo gate lint`)
// that also holds the local commit gate's own box-wide lint lock: a bare
// `golangci-lint run` reintroduced here would let CI's self-hosted runner
// collide with a local commit gate on golangci-lint's own $TMPDIR lock again
// — that lock file is opened 0600 by whichever account gets there first
// (gofrs/flock's default, no WithPermissions option — see lintlock.go), so a
// SECOND account's open fails with a permission error golangci-lint reports
// identically to ordinary contention, whether --allow-serial-runners is
// passed or not. Exactly the failure PR #740 hit.
func TestLintWorkflowStep_RunsGolangciLintThroughGateLintWrapper(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	job := lintJobBlock(t, wf)

	if !strings.Contains(job, "./bin/aphrollo gate lint run") {
		t.Fatalf("pipeline.yml's %q job does not invoke golangci-lint through `aphrollo gate lint` — it must, so CI and the local commit gate share the same box-wide lint lock", lintJobName)
	}
	for _, line := range strings.Split(job, "\n") {
		if bareGolangciLintRunRe.MatchString(line) {
			t.Fatalf("pipeline.yml's %q job runs golangci-lint DIRECTLY (%q) — it must go through `aphrollo gate lint` instead, or it bypasses the box-wide lint lock and can collide with a local commit gate again", lintJobName, strings.TrimSpace(line))
		}
	}
}

// lintJobBlock isolates the `lint` job's own YAML text: from its "  lint:"
// key to the next top-level job key (two-space indent) or EOF — the same
// shape gateEnvJobBlock (gotmpdir_workflow_pin_test.go) isolates for a
// different job, kept as its own small copy here (DAMP) rather than shared,
// so this file reads top to bottom without a jump to another test file's
// helper.
func lintJobBlock(t *testing.T, workflow string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	start := -1
	for i, line := range lines {
		if start < 0 {
			if line == "  "+lintJobName+":" {
				start = i
			}
			continue
		}
		if jobKeyLineRe.MatchString(line) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("no %q job in pipeline.yml, so this test proves nothing", lintJobName)
	}
	return strings.Join(lines[start:], "\n")
}
