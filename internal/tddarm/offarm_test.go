package tddarm

import (
	"fmt"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/config"
)

// A lane is in the off arm when its bucket (0..99, a hash of a salt of its own, the repo
// and the lane) is below the share. The buckets are pinned: a change to the hash would
// move lanes between the on and off arms in the middle of the comparison.
func TestOffBucket_IsTheSameOnEveryBox(t *testing.T) {
	for _, c := range []struct {
		repo, lane string
		want       int
	}{
		{"github.com/aphrollo/aphrollo-tools", "lane/off-arm", 33},
		{"github.com/aphrollo/aphrollo-tools", "lane/a1-redgreen-warn", 25},
		{"github.com/aphrollo/aphrollo-tools", "lane/b2", 43},
		{"github.com/acme/app", "lane/fix-parse", 63},
		{"x", "y", 63},
	} {
		if got := OffBucket(c.repo, c.lane); got != c.want {
			t.Errorf("OffBucket(%q, %q) = %d, want %d", c.repo, c.lane, got, c.want)
		}
	}
}

func TestResolveShare_ShareZeroIsNoOffArmAndTheOldAnswerStands(t *testing.T) {
	set := setting(config.BuiltIn, "warn")
	got := ResolveShare(set, "github.com/aphrollo/aphrollo-tools", "lane/a1-redgreen-warn", 0)
	want := Resolve(set, "github.com/aphrollo/aphrollo-tools", "lane/a1-redgreen-warn")
	if got != want {
		t.Errorf("share 0: %+v, want %+v", got, want)
	}
}

func TestResolveShare_ALaneBelowTheShareRunsOffAndAboveItDoesNot(t *testing.T) {
	set := setting(config.BuiltIn, "warn")
	repo := "github.com/aphrollo/aphrollo-tools"
	m := ResolveShare(set, repo, "lane/a1-redgreen-warn", 30) // bucket 25
	if m.Arm != ArmOff || m.TDD != "off" || m.Why != WhyAssigned {
		t.Errorf("bucket 25 at share 30: %+v, want the off arm, assigned", m)
	}
	m = ResolveShare(set, repo, "lane/off-arm", 30) // bucket 33
	if m.Arm == ArmOff || m.Arm == "" {
		t.Errorf("bucket 33 at share 30: %+v, want an enforce/warn arm", m)
	}
	if m := ResolveShare(set, repo, "lane/b2", 100); m.Arm != ArmOff {
		t.Errorf("share 100: %+v, want every lane off", m)
	}
}

func TestResolveShare_APinnedLaneAndTheTrunkAreNeverOff(t *testing.T) {
	repo := "github.com/aphrollo/aphrollo-tools"
	if m := ResolveShare(setting(config.Repo, "enforce"), repo, "lane/a1-redgreen-warn", 100); m.Arm == ArmOff || m.Why != WhyPinned {
		t.Errorf("a pinned lane at share 100: %+v, want pinned, outside the off arm", m)
	}
	if m := ResolveShare(setting(config.BuiltIn, "warn"), repo, "main", 100); m.Arm == ArmOff || m.Why != WhyNoLane {
		t.Errorf("main at share 100: %+v, want no lane", m)
	}
}

// The off assignment is independent of the enforce/warn one: among the lanes that hash to
// each of those arms, about the share of each is off.
func TestOffBucket_IsIndependentOfTheEnforceWarnArm(t *testing.T) {
	var off, total [2]int
	for i := range 4000 {
		lane := fmt.Sprintf("lane/fix-%d", i)
		a := 0
		if Of("github.com/acme/app", lane) == ArmWarn {
			a = 1
		}
		total[a]++
		if OffBucket("github.com/acme/app", lane) < 30 {
			off[a]++
		}
	}
	for a := range 2 {
		if r := off[a] * 100 / total[a]; r < 25 || r > 35 {
			t.Errorf("arm %d: %d of %d lanes off (%d%%), want about 30%%", a, off[a], total[a], r)
		}
	}
}

// A lane is off when its bucket is below the share, not at it: bucket 25 is on at share 25
// and off at share 26.
func TestResolveShare_TheShareIsAnExclusiveBound(t *testing.T) {
	set := setting(config.BuiltIn, "warn")
	repo, lane := "github.com/aphrollo/aphrollo-tools", "lane/a1-redgreen-warn" // bucket 25
	if m := ResolveShare(set, repo, lane, 25); m.Arm == ArmOff {
		t.Errorf("share 25 put a bucket-25 lane off: %+v", m)
	}
	if m := ResolveShare(set, repo, lane, 26); m.Arm != ArmOff {
		t.Errorf("share 26 left a bucket-25 lane on: %+v", m)
	}
}
