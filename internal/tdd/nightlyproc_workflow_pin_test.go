package tdd

import (
	"strings"
	"testing"
)

// The process tier (tests behind //go:build proc) leaves the pull request's
// run, so the one place it runs is nightly-proc.yml. A workflow that dropped
// the tag, one of the two operating systems, or the schedule would turn those
// tests into ones nothing runs, with no failure to say so.

// The tag is what selects the tier: without it the run is the pull request's
// own.
func TestNightlyProcWorkflow_RunsTheSuiteUnderTheProcTag(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-proc.yml")

	if !strings.Contains(wf, "go test -tags proc") {
		t.Fatalf("nightly-proc.yml never runs `go test -tags proc`:\n%s", wf)
	}
}

// The process tests are the ones whose behaviour differs by operating system
// (job objects against process groups), so both are what they run on.
func TestNightlyProcWorkflow_RunsOnLinuxAndWindows(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-proc.yml")

	for _, want := range []string{"ubuntu-latest", "windows-latest", "schedule:"} {
		if !strings.Contains(wf, want) {
			t.Fatalf("nightly-proc.yml carries no %q:\n%s", want, wf)
		}
	}
}

// A fork's scheduled run has nothing to say about this repository's tiers.
func TestNightlyProcWorkflow_GuardsAgainstForkRuns(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-proc.yml")

	if !strings.Contains(wf, "github.repository == 'aphrollo/aphrollo-tools'") {
		t.Fatalf("nightly-proc.yml carries no repository guard against a fork's scheduled run:\n%s", wf)
	}
}
