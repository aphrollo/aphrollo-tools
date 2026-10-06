package merge

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A hosted run hands every job the pull request it runs for, and workflows read
// it (a version check asks gh about github.event.pull_request.number). Local CI
// judges the same PR, so it names that PR's fields from the host the same way.

func init() {
	// No test reaches a real host: a test that wants a PR sets its own.
	ciPullRequestOf = func(lane string) (ciPullRequest, error) {
		return ciPullRequest{}, errors.New("no host in a test")
	}
}

func stubCIPullRequest(t *testing.T, pr ciPullRequest, err error) {
	t.Helper()
	prev := ciPullRequestOf
	ciPullRequestOf = func(string) (ciPullRequest, error) { return pr, err }
	t.Cleanup(func() { ciPullRequestOf = prev })
}

func TestLocalCI_TheWorkflowReadsTheNumberAndURLOfThePullRequestItJudges(t *testing.T) {
	root, mark := ciLane(t, `on: pull_request
jobs:
  check:
    steps:
      - run: echo "${{ github.event.pull_request.number }} ${{ github.event.pull_request.html_url }} ${{ github.event.pull_request.title }}" >> "$LOCALCI_MARK"
`)
	stubCIPullRequest(t, ciPullRequest{Number: 1239, URL: "https://github.com/o/r/pull/1239", Title: "Keep a merge moving"}, nil)

	var log bytes.Buffer
	if _, err := LocalCI(root, &log); err != nil {
		t.Fatalf("LocalCI: %v\n%s", err, log.String())
	}

	want := "1239 https://github.com/o/r/pull/1239 Keep a merge moving"
	if got := strings.TrimSpace(readFileString(t, mark)); got != want {
		t.Errorf("the workflow read %q, want %q", got, want)
	}
}

func TestLocalCI_APullRequestTheHostCannotNameIsSaidSoAndTheRunGoesOn(t *testing.T) {
	root, mark := ciLane(t, `on: pull_request
jobs:
  check:
    steps:
      - run: echo "[${{ github.event.pull_request.number }}]" >> "$LOCALCI_MARK"
`)
	stubCIPullRequest(t, ciPullRequest{}, errors.New("gh: no route to host"))

	var log bytes.Buffer
	if _, err := LocalCI(root, &log); err != nil {
		t.Fatalf("a PR that cannot be read must not fail the run: %v\n%s", err, log.String())
	}

	if got := strings.TrimSpace(readFileString(t, mark)); got != "[]" {
		t.Errorf("the workflow read %q, want an empty number", got)
	}
	if !strings.Contains(log.String(), "pull request") || !strings.Contains(log.String(), "gh: no route to host") {
		t.Errorf("the log does not say the PR could not be read:\n%s", log.String())
	}
}
