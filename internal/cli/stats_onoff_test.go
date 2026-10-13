package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func onOffSeed() map[time.Duration]tdd.Event {
	return map[time.Duration]tdd.Event{
		5 * time.Hour: {Kind: "lane-arm", Lane: "lane/on", Detail: map[string]string{"arm": "enforce", "why": "assigned", "mode": "enforce"}},
		4 * time.Hour: {Kind: "merge", Lane: "lane/on", Verdict: "ok", Detail: map[string]string{"pr": "12"}},
		3 * time.Hour: {Kind: "lane-arm", Lane: "lane/off", Detail: map[string]string{"arm": "off", "why": "assigned", "mode": "off"}},
	}
}

// With an off lane in the log, `stats --ab` adds the on vs off section and reads the
// tracker for the escapes it blames on a lane.
func TestStatsAB_AddsTheOnVsOffSectionAndReadsTheTrackerForEscapes(t *testing.T) {
	fake := &host.Fake{
		ListIssuesFn: func(host.IssueQuery) ([]host.Issue, error) {
			return []host.Issue{{Number: 40, Title: "escape: totals wrong", Body: "caused by PR #12"}}, nil
		},
	}
	prev := attributionHostFn
	attributionHostFn = func(string) (host.Host, bool) { return fake, true }
	t.Cleanup(func() { attributionHostFn = prev })

	repo := statsRepo(t, onOffSeed())
	code, out, errOut := runStatsCmd(t, "--repo", repo, "--ab")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"on vs off", "on   1 lanes  escapes 1", "off  1 lanes  escapes 0", "escapes per lane (primary)"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats --ab lacks %q:\n%s", want, out)
		}
	}
}

// A log with no off lane reads nothing from the tracker.
func TestStatsAB_ALogWithNoOffLaneAsksTheTrackerNothing(t *testing.T) {
	prev := attributionHostFn
	attributionHostFn = func(string) (host.Host, bool) {
		t.Error("the tracker was asked although no lane is off")
		return nil, false
	}
	t.Cleanup(func() { attributionHostFn = prev })
	repo := statsRepo(t, abSeed())
	if code, out, errOut := runStatsCmd(t, "--repo", repo, "--ab"); code != 0 || strings.Contains(out, "on vs off") {
		t.Errorf("exit %d, stderr %s, out %s; want no on vs off section", code, errOut, out)
	}
}
