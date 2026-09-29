package tdd

import (
	"regexp"
	"strings"
	"testing"
)

// The Windows smoke job runs on the operator's own box, registered as a repo
// runner that carries ONLY the `windows-smoke` label (no default labels, so
// the bare `runs-on: self-hosted` jobs elsewhere never land on it). The pins
// below hold the three properties that make running PR code on that box
// acceptable, plus the one step the job exists for.

// TestWindowsSmokeWorkflow_NeverRunsWithAWriteTokenOnTheBox pins the trigger:
// `pull_request_target` runs with a write token and the base repo's secrets,
// which on a personal machine is exactly the exposure the job must not have.
func TestWindowsSmokeWorkflow_NeverRunsWithAWriteTokenOnTheBox(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "windows-smoke.yml")

	if strings.Contains(wf, "pull_request_target") {
		t.Fatalf("windows-smoke.yml uses pull_request_target -- a write token on the operator's box:\n%s", wf)
	}
	if !strings.Contains(wf, "contents: read") {
		t.Fatalf("windows-smoke.yml does not narrow its token to contents: read:\n%s", wf)
	}
}

// TestWindowsSmokeWorkflow_TargetsOnlyTheWindowsSmokeRunner pins runs-on to the
// runner's single label. Adding `self-hosted` would match nothing (the runner
// was registered without default labels) and the job would queue forever;
// dropping `windows-smoke` would send it to the Linux fleet.
func TestWindowsSmokeWorkflow_TargetsOnlyTheWindowsSmokeRunner(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "windows-smoke.yml")

	runsOn := regexp.MustCompile(`(?m)^\s*runs-on:\s*(.+)$`).FindAllStringSubmatch(wf, -1)
	if len(runsOn) == 0 {
		t.Fatalf("windows-smoke.yml has no runs-on:\n%s", wf)
	}
	for _, m := range runsOn {
		if strings.TrimSpace(m[1]) != "[windows-smoke]" {
			t.Fatalf("runs-on: %s, want [windows-smoke] -- the runner carries that one label and nothing else", m[1])
		}
	}
}

// TestWindowsSmokeWorkflow_ForkPullRequestsNeverReachTheBox pins the fork
// guard: the repo is public, and a fork's pull_request would otherwise run
// arbitrary code on the operator's machine.
func TestWindowsSmokeWorkflow_ForkPullRequestsNeverReachTheBox(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "windows-smoke.yml")

	if !strings.Contains(wf, "github.event.pull_request.head.repo.full_name == github.repository") {
		t.Fatalf("windows-smoke.yml has no fork guard on its job:\n%s", wf)
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
