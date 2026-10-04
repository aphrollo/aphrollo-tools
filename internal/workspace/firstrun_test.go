package workspace

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// firstRunWorld has the merge read a PR whose runs are the ones given, newest
// first as gh lists them, and whose failed jobs are named by failed.
func firstRunWorld(t *testing.T, runs []prRun, failed []string) {
	t.Helper()
	newCIWorld(t, tdd.CIAuto, CIStatus{State: "green", SHA: "abc"})
	oRuns, oJobs := ghPRRuns, ghRunFailedJobs
	t.Cleanup(func() { ghPRRuns, ghRunFailedJobs = oRuns, oJobs })
	ghPRRuns = func(string, string) ([]prRun, error) { return runs, nil }
	ghRunFailedJobs = func(string, int64) ([]string, error) { return failed, nil }
}

func ciOf(t *testing.T) []tdd.Event { t.Helper(); return ofKind(emitted(t), "ci") }

// #1186 and #1192: the first run failed, the agent read it with gh, pushed a
// fix, and the merge only ever saw the green of the second head. The first
// run's red is recorded when the merge reads CI, at the run's own time, so it
// is the lane's first ci event and its cause is the failed job's.
func TestMergeApply_ARedFirstRunOfAnEarlierHeadIsRecordedAtItsOwnTime(t *testing.T) {
	firstRunWorld(t, []prRun{
		{ID: 2, SHA: "4628333b", Status: "completed", Conclusion: "success", CreatedAt: "2026-10-03T20:17:22Z"},
		{ID: 3, SHA: "4628333b", Status: "completed", Conclusion: "success", CreatedAt: "2026-10-03T20:17:22Z"},
		{ID: 1, SHA: "ef284346", Status: "completed", Conclusion: "failure", CreatedAt: "2026-10-03T19:43:33Z"},
		{ID: 4, SHA: "ef284346", Status: "completed", Conclusion: "success", CreatedAt: "2026-10-03T19:43:33Z"},
	}, []string{"test-windows (rest)"})
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}

	var red *tdd.Event
	for _, e := range ciOf(t) {
		if e.Detail["sha"] == "ef284346" {
			red = &e
		}
	}
	if red == nil || red.Verdict != "red" || red.At != "2026-10-03T19:43:33Z" || red.Detail["cause"] != "test" || red.Detail["pr"] != "5" {
		t.Fatalf("first-run event = %+v, want red at 19:43:33Z, cause test, PR 5", red)
	}
}

func TestMergeApply_AFirstRunStillRunningOrCancelledIsNotRecorded(t *testing.T) {
	for name, run := range map[string]prRun{
		"running":   {ID: 1, SHA: "aaaa1111", Status: "in_progress", CreatedAt: "2026-10-03T19:43:33Z"},
		"cancelled": {ID: 1, SHA: "aaaa1111", Status: "completed", Conclusion: "cancelled", CreatedAt: "2026-10-03T19:43:33Z"},
	} {
		t.Run(name, func(t *testing.T) {
			firstRunWorld(t, []prRun{run}, nil)
			if _, err := applyMerge(t, ""); err != nil {
				t.Fatal(err)
			}
			for _, e := range ciOf(t) {
				if e.Detail["sha"] == "aaaa1111" {
					t.Fatalf("recorded %+v for a first run that settled nothing", e)
				}
			}
		})
	}
}

// A first run that passed is recorded too, once: the second read dedups by sha.
func TestMergeApply_AGreenFirstRunIsRecordedOnce(t *testing.T) {
	firstRunWorld(t, []prRun{{ID: 1, SHA: "bbbb2222", Status: "completed", Conclusion: "success", CreatedAt: "2026-10-03T19:43:33Z"}}, nil)
	for range 2 {
		if _, err := applyMerge(t, ""); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, e := range ciOf(t) {
		if e.Detail["sha"] == "bbbb2222" && e.Verdict == "green" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("green events for the first run = %d, want 1", n)
	}
}
