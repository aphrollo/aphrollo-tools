package tdd

import (
	"strings"
	"testing"
)

// TestNightlyFlakeHuntWorkflow_RunsWithRaceShuffleAndTheMeasuredCount pins the
// nightly flake hunt's own `go test` invocation to the three flags a flake
// hunt is worthless without: `-race` (a data race is exactly the shape three
// of the five flakes this workflow exists for took), `-shuffle=on` (a shared
// package-load or ordering assumption only shows up once the order moves),
// and a repeat count high enough that a low-probability flake has a real
// chance to show up in one night's run.
func TestNightlyFlakeHuntWorkflow_RunsWithRaceShuffleAndTheMeasuredCount(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-flake-hunt.yml")

	for _, want := range []string{"-race", "-shuffle=on", "-count=5", "-json"} {
		if !strings.Contains(wf, want) {
			t.Fatalf("nightly-flake-hunt.yml's go test invocation does not carry %q:\n%s", want, wf)
		}
	}
}

// TestNightlyFlakeHuntWorkflow_FilesAnIssuePerFailingTest pins the workflow to
// building and running the flakehunt helper against the suite's own JSON
// output — the piece that turns a raw test failure into a filed issue, rather
// than a log line nobody reads until the next merge trips over the same test.
func TestNightlyFlakeHuntWorkflow_FilesAnIssuePerFailingTest(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-flake-hunt.yml")

	for _, want := range []string{"./tools/flakehunt", "flakehunt"} {
		if !strings.Contains(wf, want) {
			t.Fatalf("nightly-flake-hunt.yml never builds or runs the flakehunt helper (missing %q):\n%s", want, wf)
		}
	}
}

// TestNightlyFlakeHuntWorkflow_GuardsAgainstForkRuns pins the same fork guard
// nightly-fuzz.yml and nightly-mutants.yml state for the same reason: a
// fork's scheduled or dispatched run has no token to file an issue with, and
// must not try.
func TestNightlyFlakeHuntWorkflow_GuardsAgainstForkRuns(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-flake-hunt.yml")

	if !strings.Contains(wf, "github.repository == 'aphrollo/aphrollo-tools'") {
		t.Fatalf("nightly-flake-hunt.yml carries no repository guard against a fork's scheduled run:\n%s", wf)
	}
}
