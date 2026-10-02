package tdd

import (
	"regexp"
	"strings"
	"testing"
)

// TestWindowsSmokeWorkflow_NeverRunsWithAWriteTokenOnTheBox pins the trigger:
// `pull_request_target` runs a pull request's code with a write token and the
// base repo's secrets, which a smoke job has no use for.
func TestWindowsSmokeWorkflow_NeverRunsWithAWriteTokenOnTheBox(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "windows-smoke.yml")

	if strings.Contains(wf, "pull_request_target") {
		t.Fatalf("windows-smoke.yml uses pull_request_target -- a write token for pull request code:\n%s", wf)
	}
	if !strings.Contains(wf, "contents: read") {
		t.Fatalf("windows-smoke.yml does not narrow its token to contents: read:\n%s", wf)
	}
}

// ratchet: test_removed TestWindowsSmokeWorkflow_TargetsOnlyTheWindowsSmokeRunner: the job moved off the self-hosted windows-smoke runner to windows-latest, pinned below
// ratchet: test_removed TestWindowsSmokeWorkflow_ForkPullRequestsNeverReachTheBox: a GitHub-hosted runner is disposable, so fork pull requests no longer need a guard

// TestWindowsSmokeWorkflow_RunsOnAGitHubHostedWindowsRunner pins runs-on to the
// hosted Windows image. `self-hosted` would send public pull request code to
// the Linux fleet (or to any box registered with that label), and a Linux
// label would make the job pointless.
func TestWindowsSmokeWorkflow_RunsOnAGitHubHostedWindowsRunner(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "windows-smoke.yml")

	runsOn := regexp.MustCompile(`(?m)^\s*runs-on:\s*(.+)$`).FindAllStringSubmatch(wf, -1)
	if len(runsOn) == 0 {
		t.Fatalf("windows-smoke.yml has no runs-on:\n%s", wf)
	}
	for _, m := range runsOn {
		if strings.TrimSpace(m[1]) != "windows-latest" {
			t.Fatalf("runs-on: %s, want windows-latest", m[1])
		}
	}
}

// TestWindowsSmokeWorkflow_RoundTripsAnUntrackedFileThroughProbeDiscard pins
// the end-to-end step #839 needs on Windows: discarding an untracked file goes
// through `git diff --no-index /dev/null <file>`, and only a real Windows git
// shows whether that backup applies back.
func TestWindowsSmokeWorkflow_RoundTripsAnUntrackedFileThroughProbeDiscard(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "windows-smoke.yml")

	for _, want := range []string{"gate probe discard --apply", "git apply"} {
		if !strings.Contains(wf, want) {
			t.Fatalf("windows-smoke.yml's round trip is missing %q:\n%s", want, wf)
		}
	}
}
