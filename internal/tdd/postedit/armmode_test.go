package postedit

import (
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

func armSetup(t *testing.T) (linked string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("TRELLIS_CONFIG", t.TempDir())
	_, linked = primaryRepo(t)
	return linked
}

// A lane of a repo that pins nothing is in the arm its repo and name hash to, read
// from the files of the checkout alone.
func TestEffectiveTDD_AnUnpinnedLaneRunsUnderItsAssignedArm(t *testing.T) {
	linked := armSetup(t)
	m := EffectiveTDD(filepath.Join(linked, "sub", "dir"))
	want := tddarm.Of(tddarm.RepoKey(linked), "lane/x")
	if m.Arm != want || m.TDD != want || m.Why != tddarm.WhyAssigned {
		t.Errorf("EffectiveTDD = %+v, want lane/x in the %s arm, assigned", m, want)
	}
}

func TestEffectiveTDD_ARepoThatPinsTddIsOutsideTheExperiment(t *testing.T) {
	linked := armSetup(t)
	mustWrite(t, filepath.Join(linked, "trellis.toml"), "tdd = \"off\"\n")
	m := EffectiveTDD(linked)
	if m.TDD != "off" || m.Arm != "" || m.Why != tddarm.WhyPinned || m.Layer != "repo" {
		t.Errorf("EffectiveTDD = %+v, want a repo pin of off with no arm", m)
	}
}

// The lane's arm is an event written when the lane is first seen, and only then.
func TestRecordLaneArm_WritesOneEventForTheLaneHoweverOftenItIsSeen(t *testing.T) {
	linked := armSetup(t)
	m := EffectiveTDD(linked)
	RecordLaneArm(linked, "s-arm", m)
	RecordLaneArm(linked, "s-arm", m)
	got := eventsOfKind(linked, "lane-arm")
	if len(got) != 1 {
		t.Fatalf("%d lane-arm events, want 1: %+v", len(got), got)
	}
	e := got[0]
	if e.Lane != "lane/x" || e.Actor != "s-arm" || e.Detail["arm"] != m.Arm || e.Detail["why"] != "assigned" || e.Detail["mode"] != m.TDD {
		t.Errorf("event = %+v, want lane/x, the assigned arm %s and its mode", e, m.Arm)
	}
}

func TestRecordLaneArm_APinnedLaneIsRecordedAsPinnedWithNoArm(t *testing.T) {
	linked := armSetup(t)
	mustWrite(t, filepath.Join(linked, "trellis.toml"), "tdd = \"enforce\"\n")
	RecordLaneArm(linked, "s-arm", EffectiveTDD(linked))
	got := eventsOfKind(linked, "lane-arm")
	if len(got) != 1 || got[0].Detail["why"] != "pinned" || got[0].Detail["arm"] != "" || got[0].Detail["mode"] != "enforce" {
		t.Errorf("events = %+v, want one pinned enforce with no arm", got)
	}
}

// No lane (a trunk branch), no record: there is nothing to join outcomes by.
func TestRecordLaneArm_ATrunkCheckoutWritesNothing(t *testing.T) {
	armSetup(t)
	primary, _ := primaryRepo(t)
	RecordLaneArm(primary, "s-arm", EffectiveTDD(primary))
	if got := eventsOfKind(primary, "lane-arm"); len(got) != 0 {
		t.Errorf("events = %+v, want none for the primary checkout on main", got)
	}
}
