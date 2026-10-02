package merge

import (
	"bytes"
	"strings"
	"testing"
)

func TestLocalCI_TheSummaryLineCountsRanAndSkippedJobs(t *testing.T) {
	root, _ := ciLane(t, `on: pull_request
jobs:
  good:
    steps:
      - run: echo ok
  bad:
    steps:
      - run: exit 1
  never:
    if: github.event_name == 'push'
    steps:
      - run: echo never
`)
	var log bytes.Buffer
	if _, err := LocalCI(root, &log); err == nil {
		t.Fatal("the failing job must fail local CI")
	}
	if !strings.Contains(log.String(), "ci local: red — 2 job(s) ran, 1 skipped") {
		t.Errorf("the summary must count the successful and the failed job together:\n%s", log.String())
	}
}
