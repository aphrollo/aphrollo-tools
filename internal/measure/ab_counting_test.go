package measure

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const abRepoKey = "example.com/org/repo"

func gateAt(sec float64, lane, kind, verdict string) tdd.Event {
	return ev(sec, lane, kind, func(e *tdd.Event) { e.Verdict = verdict })
}

// A lane made by a plain `git worktree add` records no lane-arm event and may never
// write a code file the hook judges; the arm is a pure function of repo and lane name,
// so the read puts it in its arm anyway.
func TestComputeAB_ALaneWithNoArmEventIsAssignedItsArmAtReadTime(t *testing.T) {
	events := []tdd.Event{
		gateAt(0, "alpha", "commit_gate", "green"),  // FNV arm: enforce
		gateAt(1, "lane-1", "commit_gate", "green"), // FNV arm: warn
		gateAt(2, "main", "commit_gate", "green"),   // trunk: in no arm
	}
	ab := computeAB(events, Options{RepoKey: abRepoKey})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes != 1 || w.Lanes != 1 {
		t.Errorf("enforce %d lanes, warn %d lanes, want 1 and 1 (alpha, lane-1; main in neither)", e.Lanes, w.Lanes)
	}
}

// Without a repo key the read cannot hash, and counts only the lanes that name an arm.
func TestComputeAB_WithoutARepoKeyALaneWithNoArmEventIsLeftOut(t *testing.T) {
	ab := computeAB([]tdd.Event{gateAt(0, "alpha", "commit_gate", "green")}, Options{})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes+w.Lanes != 0 {
		t.Errorf("lanes = %d, want 0 with no repo key", e.Lanes+w.Lanes)
	}
}

// A recorded arm wins over the hash: a pin recorded as a pin stays out of both arms.
func TestComputeAB_ARecordedPinIsNotReassignedAtReadTime(t *testing.T) {
	events := []tdd.Event{ev(0, "alpha", "lane-arm", detail("why", "pinned", "mode", "enforce"))}
	ab := computeAB(events, Options{RepoKey: abRepoKey})
	if ab.Pinned != 1 || abRow(t, ab, "enforce").Lanes != 0 || abRow(t, ab, "warn").Lanes != 0 {
		t.Errorf("pinned %d, arms %+v, want the pinned lane in neither arm", ab.Pinned, ab.Arms)
	}
}

// ariadne merges on a <lane>-merge branch: its premerge gate names that branch, and it
// is the lane's merge, not a lane of its own. Its green ends the lane's time to green.
func TestComputeAB_AMergeBranchIsTheSameLaneAsItsBase(t *testing.T) {
	events := []tdd.Event{
		ev(0, "alpha", "lane-arm", detail("arm", "enforce", "why", "assigned", "mode", "enforce")),
		shadowAt(1, "alpha", "red-green", "trellis-stricter", "aphrollo", "block", "arm", "enforce", "arm_why", "assigned", "lang", "go"),
		gateAt(100, "alpha-merge", "merge_gate", "green"),
	}
	ab := computeAB(events, Options{RepoKey: abRepoKey})
	e := abRow(t, ab, "enforce")
	if e.Lanes != 1 {
		t.Errorf("enforce lanes = %d, want 1: alpha and alpha-merge are one lane", e.Lanes)
	}
	if e.TimeToGreen.N != 1 || e.TimeToGreen.P50 != 99 {
		t.Errorf("time to green = %+v, want n 1, p50 99s (the merge gate's green)", e.TimeToGreen)
	}
}

// charlie hashes to warn and charlie-merge to enforce: a branch the merge gate fired on
// is charlie, whether or not charlie has events of its own.
func TestComputeAB_AMergeGateBranchTakesItsBaseLanesArm(t *testing.T) {
	ab := computeAB([]tdd.Event{gateAt(0, "charlie-merge", "merge_gate", "green")}, Options{RepoKey: abRepoKey})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes != 0 || w.Lanes != 1 {
		t.Errorf("enforce %d, warn %d lanes, want 0 and 1 (the base lane's arm)", e.Lanes, w.Lanes)
	}
}

// A lane that merely ends in -merge, with no base lane and no merge gate on it, is its
// own lane (lane/996-argv-merge is a lane's name, not a merge branch).
func TestComputeAB_ALaneEndingInMergeWithNoBaseKeepsItsOwnName(t *testing.T) {
	ab := computeAB([]tdd.Event{gateAt(0, "charlie-merge", "commit_gate", "green")}, Options{RepoKey: abRepoKey})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes != 1 || w.Lanes != 0 {
		t.Errorf("enforce %d, warn %d lanes, want 1 and 0 (charlie-merge hashes to enforce)", e.Lanes, w.Lanes)
	}
}

// n 0 on time to green means two different things; the printout says which.
func TestAB_TextSaysWhetherNoRedOccurredOrNothingWasRecorded(t *testing.T) {
	none := computeAB([]tdd.Event{gateAt(0, "alpha", "commit_gate", "green")}, Options{RepoKey: abRepoKey})
	if got := none.Text(); !strings.Contains(got, "time to green n 0 (no red occurred: no deny or warning)") {
		t.Errorf("no-red text:\n%s", got)
	}
	dropped := computeAB([]tdd.Event{
		shadowAt(0, "alpha", "red-green", "unjudged", "cause", "budget", "arm", "enforce", "arm_why", "assigned"),
	}, Options{RepoKey: abRepoKey})
	if got := dropped.Text(); !strings.Contains(got, "time to green n 0 (not recorded: 1 decision(s) dropped for the budget") {
		t.Errorf("dropped text:\n%s", got)
	}
	noGreen := computeAB([]tdd.Event{
		ev(0, "alpha", "lane-arm", detail("arm", "enforce", "why", "assigned")),
		shadowAt(1, "alpha", "red-green", "trellis-stricter", "aphrollo", "block", "arm", "enforce", "arm_why", "assigned"),
	}, Options{RepoKey: abRepoKey})
	if got := noGreen.Text(); !strings.Contains(got, "time to green n 0 (red recorded, no green after it yet)") {
		t.Errorf("no-green text:\n%s", got)
	}
}
