package postedit

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// armSetupBoth is armSetup with the primary checkout too, whose aphrollo.toml is where the
// off share is read from.
func armSetupBoth(t *testing.T) (primary, linked string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("TRELLIS_CONFIG", t.TempDir())
	return primaryRepo(t)
}

func declareShare(t *testing.T, dir, share string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "aphrollo.toml"), "[aphrollo]\nab-off-share = "+share+"\n")
}

// A repo that declares ab-off-share = 100 puts every unpinned lane in the off arm.
func TestLaneOff_ARepoWithAFullOffShareRunsItsLanesWithAphrolloOff(t *testing.T) {
	primary, linked := armSetupBoth(t)
	declareShare(t, primary, "100")
	m := EffectiveTDD(filepath.Join(linked, "sub"))
	if m.Arm != tddarm.ArmOff || m.TDD != "off" || m.Why != tddarm.WhyAssigned {
		t.Errorf("EffectiveTDD = %+v, want the off arm, assigned", m)
	}
	if !EffectiveTDD(filepath.Join(linked, "sub")).Off() {
		t.Errorf("Off = false, want true for a lane in the off arm")
	}
}

func TestLaneOff_NoShareMeansNoLaneIsOffAndAPinKeepsItOn(t *testing.T) {
	primary, linked := armSetupBoth(t)
	if EffectiveTDD(linked).Off() {
		t.Errorf("Off with no ab-off-share = true, want false")
	}
	declareShare(t, primary, "100")
	mustWrite(t, filepath.Join(linked, "trellis.toml"), "tdd = \"enforce\"\n")
	if EffectiveTDD(linked).Off() {
		t.Errorf("Off for a lane that pins tdd = true, want false: a pin is outside the experiment")
	}
}

// A lane pinned off by tdd = "off" is not in the off arm: only the red-green rule is off there.
func TestLaneOff_APinnedTddOffIsNotTheOffArm(t *testing.T) {
	primary, linked := armSetupBoth(t)
	declareShare(t, primary, "100")
	mustWrite(t, filepath.Join(linked, "trellis.toml"), "tdd = \"off\"\n")
	if EffectiveTDD(linked).Off() {
		t.Errorf("Off for tdd = off pinned, want false")
	}
}

func TestRecordLaneArm_AnOffLaneIsRecordedOnceWithModeOff(t *testing.T) {
	primary, linked := armSetupBoth(t)
	declareShare(t, primary, "100")
	m := EffectiveTDD(linked)
	RecordLaneArm(linked, "s-off", m)
	RecordLaneArm(linked, "s-off", m)
	got := eventsOfKind(linked, "lane-arm")
	if len(got) != 1 || got[0].Detail["mode"] != "off" || got[0].Detail["arm"] != "off" || got[0].Detail["why"] != "assigned" {
		t.Errorf("events = %+v, want one lane-arm with mode off, arm off", got)
	}
}

// The share a lane's own worktree declares is never read: a rebase that brings one in, or
// a lane that edits its own config, changes nothing.
func TestLaneOff_ALanesOwnWorktreeConfigNeverPutsItOff(t *testing.T) {
	_, linked := armSetupBoth(t)
	declareShare(t, linked, "100") // a lane editing its own config, or a rebase bringing the share in
	if EffectiveTDD(linked).Off() {
		t.Errorf("a lane that set its own ab-off-share = 100 went off; only the primary checkout's declaration counts")
	}
}

// Once a lane's arm is recorded, the record decides: a rebase that brings a share into the
// lane's own tree does not flip it, and neither does a share raised on trunk afterwards.
func TestLaneOff_ARecordedOnLaneStaysOnWhateverTheShareBecomes(t *testing.T) {
	primary, linked := armSetupBoth(t)
	RecordLaneArm(linked, "s", EffectiveTDD(linked)) // first sight, no share declared: on
	declareShare(t, linked, "100")
	declareShare(t, primary, "100")
	if m := EffectiveTDD(linked); m.Off() {
		t.Errorf("a lane recorded as on went off after the share was raised: %+v", m)
	}
}

func TestLaneOff_ARecordedOffLaneStaysOffWhenTheShareIsLowered(t *testing.T) {
	primary, linked := armSetupBoth(t)
	declareShare(t, primary, "100")
	RecordLaneArm(linked, "s", EffectiveTDD(linked))
	declareShare(t, primary, "0")
	if m := EffectiveTDD(linked); !m.Off() {
		t.Errorf("a lane recorded as off came back on when the share was lowered: %+v", m)
	}
}

// A lane first seen after the share was raised is off; one seen before it is not.
func TestLaneOff_ANewLaneAfterTheShareIsRaisedIsOffWhileTheOldOneIsNot(t *testing.T) {
	primary, linked := armSetupBoth(t)
	RecordLaneArm(linked, "s", EffectiveTDD(linked))
	declareShare(t, primary, "100")
	fresh := filepath.Join(t.TempDir(), "fresh")
	if out, err := exec.Command("git", "-C", primary, "worktree", "add", "-q", "-b", "lane/y", fresh).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	if !EffectiveTDD(fresh).Off() {
		t.Errorf("a lane first seen after the share was raised is not off")
	}
	if EffectiveTDD(linked).Off() {
		t.Errorf("the lane seen before the share was raised went off")
	}
}

// A lane is one lane whichever directory of its checkout a hook stands in: recorded from a
// subdirectory and from the root it is one event, and the record holds for both.
func TestRecordLaneArm_ASubdirectoryAndTheRootRecordTheLaneOnce(t *testing.T) {
	primary, linked := armSetupBoth(t)
	sub := filepath.Join(linked, "pkg")
	mustWrite(t, filepath.Join(sub, "x.go"), "package pkg\n")
	declareShare(t, primary, "100")
	RecordLaneArm(sub, "s", EffectiveTDD(sub))
	RecordLaneArm(linked, "s", EffectiveTDD(linked))
	if got := eventsOfKind(linked, "lane-arm"); len(got) != 1 {
		t.Fatalf("%d lane-arm events, want 1: %+v", len(got), got)
	}
	declareShare(t, primary, "0")
	if !EffectiveTDD(linked).Off() || !EffectiveTDD(sub).Off() {
		t.Errorf("the lane recorded off from a subdirectory is not off from both directories")
	}
}
