package cli

import (
	"regexp"
	"strings"
	"testing"
)

// Every progress line `workspace merge --wait` prints carries the UTC time it
// began, so the gaps between its stages can be measured from the log alone.
// The line itself is kept whole after the stamp (the plan above it is not one
// of them).
func TestWorkspaceMergeWait_ProgressLinesCarryAUTCTimestamp(t *testing.T) {
	_, live, _ := mergeWaitRepo(t)
	routeGhStub(t,
		ghRoute{Match: "head=o:lane/live", Out: "5"},
		ghRoute{Match: "repos/o/r/pulls/5", Out: livePR},
		ghRoute{Match: "check-runs"},
		ghRoute{Match: "/status"},
	)

	_, stdout, _ := runMergeWait(t, live, "--timeout", "1ms")

	stamped := regexp.MustCompile(`^\d\d:\d\d:\d\d   \[wait\] PR #5 0123456: `)
	n := 0
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, "[wait]") {
			n++
			if !stamped.MatchString(l) {
				t.Errorf("a [wait] line without its HH:MM:SS stamp: %q", l)
			}
		}
	}
	if n == 0 {
		t.Fatalf("no [wait] line was printed:\n%s", stdout)
	}
}
