package mutation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// How the box that cannot measure reaches the box that can.
//
// The channel is the CI job's own artifact, not a new one: `mutants-verdict`
// in .github/workflows/pipeline.yml runs on the same self-hosted Linux runner
// as `gate-env` and `test`, on the merge GitHub already computes for the PR,
// and uploads its report under a name carrying the tree id. Nothing is pushed
// at this box, nothing is polled on a schedule, and there is no store to go
// stale: an artifact keyed on a tree id is either a measurement of the tree
// being judged or it is not, which is the one question consumeRunnerReport
// asks.
//
// Every failure here answers "no measurement of this tree is available", with
// the reason. That is deliberately the same outcome as "the run has not
// finished" — a missing measurement blocks nothing, so a gh that is absent,
// unauthenticated or offline costs a merge nothing but its mutation evidence,
// which it did not have either way.

// runnerReportFn is the seam the gate reaches a runner's measurement through.
// A var so every test above states what the runner answered without a
// network, a token or gh on the box.
var runnerReportFn = fetchRunnerReport

// setRunnerReportForTest replaces that seam for one test.
func setRunnerReportForTest(fn func(root, tree string) (RunnerReport, string)) (restore func()) {
	prev := runnerReportFn
	runnerReportFn = fn
	return func() { runnerReportFn = prev }
}

// runnerFetchTimeout bounds each of the two gh calls. The pre-merge gate is
// in the foreground of a merge somebody is waiting on, and a hung metadata
// request must cost that merge its evidence rather than its whole run.
const runnerFetchTimeout = 90 * time.Second

// fetchRunnerReport asks GitHub for the measurement published for this tree.
// absent is non-empty exactly when there is no report, and says which of the
// ordinary reasons it is.
func fetchRunnerReport(root, tree string) (report RunnerReport, absent string) {
	if !ghAvailable() {
		return RunnerReport{}, "the GitHub CLI is not installed here, so no published measurement could be read"
	}
	if !hasGitHubRemote(root) {
		return RunnerReport{}, "this checkout has no GitHub remote, so there is no runner publishing measurements"
	}
	name := runnerArtifactName(tree)
	// The artifacts endpoint filters on the name itself, so the lookup is
	// one request whether the repo has published ten reports or ten
	// thousand. `.artifacts[0]` is the newest: a tree re-measured by a
	// re-run publishes a second artifact under the same name, and the later
	// run is the one to read.
	runID, err := runGhTimeout(root, runnerFetchTimeout, "api",
		"repos/{owner}/{repo}/actions/artifacts?per_page=1&name="+name,
		"--jq", ".artifacts[0].workflow_run.id // empty")
	if err != nil {
		return RunnerReport{}, "the published measurements could not be listed: " + err.Error()
	}
	if runID = strings.TrimSpace(runID); runID == "" {
		return RunnerReport{}, "no run has published a measurement of this tree yet (no artifact " + name + ")"
	}
	dir, err := os.MkdirTemp("", "mutants-verdict-")
	if err != nil {
		return RunnerReport{}, "nowhere to download the measurement to: " + err.Error()
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if _, err := runGhTimeout(root, runnerFetchTimeout, "run", "download", runID, "-n", name, "-D", dir); err != nil {
		return RunnerReport{}, "the measurement published by run " + runID + " could not be downloaded: " + err.Error()
	}
	return readRunnerReport(filepath.Join(dir, runnerReportFile))
}

// readRunnerReport reads one downloaded report. A report that cannot be
// parsed is a report that is not there: nothing is guessed out of a partial
// one, since every field of it is either the binding or a count a merge is
// judged on.
func readRunnerReport(path string) (RunnerReport, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RunnerReport{}, "the downloaded measurement could not be read: " + err.Error()
	}
	var r RunnerReport
	if err := json.Unmarshal(data, &r); err != nil {
		return RunnerReport{}, "the downloaded measurement could not be read: " + err.Error()
	}
	return r, ""
}
