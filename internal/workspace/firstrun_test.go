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
	ghPRRuns = func(string, string, int) ([]prRun, error) { return runs, nil }
	ghRunFailedJobs = func(string, int64, int) ([]string, error) { return failed, nil }
}

func ciOf(t *testing.T) []tdd.Event { t.Helper(); return ofKind(emitted(t), "ci") }

// #1186 and #1192: the first run failed, the agent read it with gh, pushed a
// fix, and the merge only ever saw the green of the second head. The first
// run's red is recorded when the merge reads CI, at the run's own time, so it
// is the lane's first ci event and its cause is the failed job's.
func TestMergeApply_ARedFirstRunOfAnEarlierHeadIsRecordedAtItsOwnTime(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
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
	t.Setenv("TRELLIS_DATA", t.TempDir())
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
	t.Setenv("TRELLIS_DATA", t.TempDir())
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

// Another run of the first head still going does not hide the run of it that
// already failed: the head is red, and a later green is not the first.
func TestMergeApply_AFailedRunOfTheFirstHeadIsRedWhileAnotherRunIsStillGoing(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	firstRunWorld(t, []prRun{
		{ID: 1, SHA: "cccc3333", Status: "completed", Conclusion: "failure", CreatedAt: "2026-10-03T19:43:33Z"},
		{ID: 2, SHA: "cccc3333", Status: "in_progress", CreatedAt: "2026-10-03T19:43:34Z"},
		{ID: 3, SHA: "dddd4444", Status: "completed", Conclusion: "success", CreatedAt: "2026-10-03T20:17:22Z"},
	}, []string{"test"})
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}
	var got *tdd.Event
	for _, e := range ciOf(t) {
		if e.Detail["sha"] == "cccc3333" {
			got = &e
		}
	}
	if got == nil || got.Verdict != "red" || got.Detail["cause"] != "test" {
		t.Fatalf("first-head event = %+v, want red with cause test", got)
	}
}

// The runs are asked for by the PR's own number, so a reused branch name or a
// busy branch's page limit cannot make another PR's run the first one.
func TestMergeApply_TheFirstRunsAreAskedForByThePRNumber(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	firstRunWorld(t, nil, nil)
	var gotBranch string
	var gotPR int
	ghPRRuns = func(_, branch string, pr int) ([]prRun, error) { gotBranch, gotPR = branch, pr; return nil, nil }
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}
	if gotBranch != "feat/z" || gotPR != 5 {
		t.Fatalf("runs asked for branch %q PR %d, want feat/z PR 5", gotBranch, gotPR)
	}
}

// A first-head failure fixed with `gh run rerun` lists as the green of its
// latest attempt; the first attempt is what "first run" means.
func TestMergeApply_AFirstRunFailureFixedByARerunIsStillRed(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	firstRunWorld(t, []prRun{
		{ID: 1, SHA: "eeee5555", Status: "completed", Conclusion: "success", CreatedAt: "2026-10-03T19:43:33Z", Attempt: 2},
	}, []string{"test"})
	oAttempt := ghRunFirstAttempt
	t.Cleanup(func() { ghRunFirstAttempt = oAttempt })
	ghRunFirstAttempt = func(_ string, id int64) (string, string, error) {
		if id != 1 {
			t.Errorf("attempt 1 read for run %d, want run 1", id)
		}
		return "completed", "failure", nil
	}
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}
	for _, e := range ciOf(t) {
		if e.Detail["sha"] == "eeee5555" {
			if e.Verdict != "red" || e.Detail["cause"] != "test" {
				t.Fatalf("event = %+v, want red with cause test", e)
			}
			return
		}
	}
	t.Fatal("no first-run event recorded")
}
