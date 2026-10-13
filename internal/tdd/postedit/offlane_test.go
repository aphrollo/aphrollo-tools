package postedit

import (
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// A repo that declares ab-off-share = 100 puts every unpinned lane in the off arm.
func TestLaneOff_ARepoWithAFullOffShareRunsItsLanesWithAphrolloOff(t *testing.T) {
	linked := armSetup(t)
	mustWrite(t, filepath.Join(linked, "aphrollo.toml"), "[aphrollo]\nab-off-share = 100\n")
	m := EffectiveTDD(filepath.Join(linked, "sub"))
	if m.Arm != tddarm.ArmOff || m.TDD != "off" || m.Why != tddarm.WhyAssigned {
		t.Errorf("EffectiveTDD = %+v, want the off arm, assigned", m)
	}
	if !EffectiveTDD(filepath.Join(linked, "sub")).Off() {
		t.Errorf("Off = false, want true for a lane in the off arm")
	}
}

func TestLaneOff_NoShareMeansNoLaneIsOffAndAPinKeepsItOn(t *testing.T) {
	linked := armSetup(t)
	if EffectiveTDD(linked).Off() {
		t.Errorf("Off with no ab-off-share = true, want false")
	}
	mustWrite(t, filepath.Join(linked, "aphrollo.toml"), "[aphrollo]\nab-off-share = 100\n")
	mustWrite(t, filepath.Join(linked, "trellis.toml"), "tdd = \"enforce\"\n")
	if EffectiveTDD(linked).Off() {
		t.Errorf("Off for a lane that pins tdd = true, want false: a pin is outside the experiment")
	}
}

// A lane pinned off by tdd = "off" is not in the off arm: only the red-green rule is off there.
func TestLaneOff_APinnedTddOffIsNotTheOffArm(t *testing.T) {
	linked := armSetup(t)
	mustWrite(t, filepath.Join(linked, "trellis.toml"), "tdd = \"off\"\n")
	if EffectiveTDD(linked).Off() {
		t.Errorf("Off for tdd = off pinned, want false")
	}
}

func TestRecordLaneArm_AnOffLaneIsRecordedOnceWithModeOff(t *testing.T) {
	linked := armSetup(t)
	mustWrite(t, filepath.Join(linked, "aphrollo.toml"), "[aphrollo]\nab-off-share = 100\n")
	m := EffectiveTDD(linked)
	RecordLaneArm(linked, "s-off", m)
	RecordLaneArm(linked, "s-off", m)
	got := eventsOfKind(linked, "lane-arm")
	if len(got) != 1 || got[0].Detail["mode"] != "off" || got[0].Detail["arm"] != "off" || got[0].Detail["why"] != "assigned" {
		t.Errorf("events = %+v, want one lane-arm with mode off, arm off", got)
	}
}
