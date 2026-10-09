package measure

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const abRepoKey = "example.com/org/repo"

func gateAt(sec float64, lane, kind, verdict string) tdd.Event {
	return ev(sec, lane, kind, func(e *tdd.Event) { e.Verdict, e.BinVer = verdict, "1.40.0" })
}

// abStarted is the first lane-arm event of a repo, a pinned lane that keeps both arms' counts
// clean: the A/B started at sec -5, on binary 1.40.0.
func abStarted() tdd.Event {
	return ev(-5, "starter", "lane-arm", func(e *tdd.Event) {
		e.BinVer = "1.40.0"
		detail("why", "pinned", "mode", "enforce")(e)
	})
}

// A lane made by a plain `git worktree add` records no lane-arm event and may never
// write a code file the hook judges; the arm is a pure function of repo and lane name,
// so the read puts it in its arm anyway.
func TestComputeAB_ALaneWithNoArmEventIsAssignedItsArmAtReadTime(t *testing.T) {
	events := []tdd.Event{
		abStarted(),
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
	ab := computeAB([]tdd.Event{abStarted(), gateAt(0, "charlie-merge", "merge_gate", "green")}, Options{RepoKey: abRepoKey})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes != 0 || w.Lanes != 1 {
		t.Errorf("enforce %d, warn %d lanes, want 0 and 1 (the base lane's arm)", e.Lanes, w.Lanes)
	}
}

// A lane that merely ends in -merge, with no base lane and no merge gate on it, is its
// own lane (lane/996-argv-merge is a lane's name, not a merge branch).
func TestComputeAB_ALaneEndingInMergeWithNoBaseKeepsItsOwnName(t *testing.T) {
	ab := computeAB([]tdd.Event{abStarted(), gateAt(0, "charlie-merge", "commit_gate", "green")}, Options{RepoKey: abRepoKey})
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

// plainLane is a lane a plain git worktree made, seen by a gate of binary ver at sec.
func plainLane(sec float64, lane, ver string) tdd.Event {
	e := gateAt(sec, lane, "commit_gate", "green")
	e.BinVer = ver
	return e
}

// Before the A/B started the arm acted nowhere: a lane from then is in neither arm, and the
// readout says how many. bravo hashes to enforce, charlie to warn.
func TestComputeAB_LanesFromBeforeTheABStartedAreInNeitherArm(t *testing.T) {
	events := []tdd.Event{
		plainLane(-100, "alpha", "1.30.0"), // old lane, old binary
		plainLane(-90, "delta", "1.40.0"),  // old lane, but before the first lane-arm event
		abStarted(),
		plainLane(10, "bravo", "1.40.0"),
		plainLane(11, "charlie", "1.40.0"),
	}
	ab := computeAB(events, Options{RepoKey: abRepoKey})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes != 1 || w.Lanes != 1 || ab.BeforeStart != 2 {
		t.Errorf("enforce %d, warn %d, before the start %d, want 1, 1 and 2", e.Lanes, w.Lanes, ab.BeforeStart)
	}
	if got := ab.Text(); !strings.Contains(got, "before the A/B started: 2 lanes") {
		t.Errorf("text lacks the before-the-start line:\n%s", got)
	}
}

// A lane whose first event is the start itself is in: the boundary is inclusive.
func TestComputeAB_ALaneFirstSeenAtTheStartIsCounted(t *testing.T) {
	starter := abStarted()
	first := plainLane(-5, "charlie", "1.40.0") // the same instant as the first lane-arm event
	ab := computeAB([]tdd.Event{starter, first}, Options{RepoKey: abRepoKey})
	if w := abRow(t, ab, "warn"); w.Lanes != 1 || ab.BeforeStart != 0 {
		t.Errorf("warn %d lanes, before the start %d, want 1 and 0", w.Lanes, ab.BeforeStart)
	}
}

// After the start, a lane whose gate events all came from an older binary never met the
// arm either; so does one whose events carry no version.
func TestComputeAB_ALaneOfOnlyOlderOrUnversionedBinariesIsInNeitherArm(t *testing.T) {
	ab := computeAB([]tdd.Event{abStarted(), plainLane(10, "bravo", "1.39.9"), plainLane(11, "charlie", ""),
		ev(12, "delta", "ci", func(e *tdd.Event) { e.BinVer = "1.40.0" })}, Options{RepoKey: abRepoKey})
	n := abRow(t, ab, "enforce").Lanes + abRow(t, ab, "warn").Lanes
	if n != 0 || ab.BeforeStart != 3 {
		t.Errorf("lanes in arms %d, before the start %d, want 0 and 3 (a ci event is no gate event)", n, ab.BeforeStart)
	}
}

// With no lane-arm event in the log the A/B starts at the first event of a binary that has it.
func TestComputeAB_WithNoLaneArmEventTheABStartsAtTheFirstBinaryThatHasIt(t *testing.T) {
	ab := computeAB([]tdd.Event{plainLane(-50, "alpha", "1.29.0"), plainLane(10, "bravo", "1.30.1"), plainLane(11, "charlie", "1.31.0")},
		Options{RepoKey: abRepoKey})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes != 1 || w.Lanes != 1 || ab.BeforeStart != 1 {
		t.Errorf("enforce %d, warn %d, before %d, want 1, 1 and 1", e.Lanes, w.Lanes, ab.BeforeStart)
	}
}
