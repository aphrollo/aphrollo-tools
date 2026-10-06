package ghworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A workflow reads the pull request it runs for through github.event.pull_request:
// its number, URL, title and draft state. The caller that knows the PR puts those
// in the event, and a step sees them as GitHub would show them.
func TestRun_TheEventsPullRequestFieldsReachTheWorkflowsExpressions(t *testing.T) {
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  info:
    steps:
      - run: echo "${{ github.event.pull_request.number }}|${{ github.event.pull_request.html_url }}|${{ github.event.pull_request.title }}|${{ github.event.pull_request.draft }}|${{ github.ref }}" >> log.txt
`, func(o *Options) {
		o.Event["pr_number"] = 77
		o.Event["pr_url"] = "https://github.com/o/r/pull/77"
		o.Event["pr_title"] = "Fix the wait"
	})

	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	want := "77|https://github.com/o/r/pull/77|Fix the wait|false|refs/pull/77/merge\n"
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestRun_WithoutAPullRequestItsFieldsAreEmptyNotInvented(t *testing.T) {
	_, _, dir := runFlow(t, `
on: pull_request
jobs:
  info:
    steps:
      - run: echo "[${{ github.event.pull_request.number }}]" >> log.txt
`)

	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "[]\n" {
		t.Errorf("log = %q, want an empty number when the caller named no PR", got)
	}
}

// Test code under a job nests its own temp dirs inside RUNNER_TEMP, and git
// refuses a GIT_DIR past the Windows path limit. The run's scratch, and so every
// job's temp root, is made under a base the box names short, and is removed with
// the run.
func TestRun_EveryJobsTempRootIsUnderTheShortScratchBaseAndGoneAfterTheRun(t *testing.T) {
	base := t.TempDir()
	defer SetScratchBaseForTest(base)()

	_, out, dir := runFlow(t, `
on: pull_request
jobs:
  first:
    steps:
      - run: echo "$RUNNER_TEMP" >> log.txt
  second:
    steps:
      - run: echo "$RUNNER_TEMP" >> log.txt
`)

	lines := strings.Fields(readFile(t, filepath.Join(dir, "log.txt")))
	if len(lines) != 2 {
		t.Fatalf("log = %q\n%s", lines, out)
	}
	for _, l := range lines {
		if !strings.HasPrefix(slashed(l), slashed(base)) {
			t.Errorf("RUNNER_TEMP %q is not under the scratch base %q", l, base)
		}
	}
	left, err := os.ReadDir(base)
	if err != nil || len(left) != 0 {
		t.Errorf("the run left %v (err %v) under its scratch base", left, err)
	}
}

// slashed is a path spelled the one way a comparison can read: forward slashes,
// lower case.
func slashed(p string) string { return strings.ToLower(strings.ReplaceAll(p, `\`, "/")) }
