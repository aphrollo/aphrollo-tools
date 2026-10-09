package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func abSeed() map[time.Duration]tdd.Event {
	return map[time.Duration]tdd.Event{
		3 * time.Hour: {Kind: "lane-arm", Lane: "lane/a", Detail: map[string]string{"arm": "warn", "why": "assigned", "mode": "warn"}},
		2 * time.Hour: {Kind: "shadow", Lane: "lane/a", Detail: map[string]string{
			"rule": "red-green", "relation": "trellis-stricter", "aphrollo": "warn", "arm": "warn", "arm_why": "assigned", "lang": "go"}},
		time.Hour: {Kind: "lane-arm", Lane: "lane/b", Detail: map[string]string{"arm": "enforce", "why": "assigned", "mode": "enforce"}},
	}
}

func TestStatsAB_PrintsEachArmWithItsLanesAndWhetherItReachedThirty(t *testing.T) {
	repo := statsRepo(t, abSeed())
	code, out, errOut := runStatsCmd(t, "--repo", repo, "--ab")
	if code != 0 {
		t.Fatalf("stats --ab exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"enforce", "warn", "1 of 30 lanes", "warnings 1", "not decidable yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats --ab lacks %q:\n%s", want, out)
		}
	}
}

func TestStatsAB_AsJSONIsTheReadout(t *testing.T) {
	repo := statsRepo(t, abSeed())
	code, out, errOut := runStatsCmd(t, "--repo", repo, "--ab", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	var ab measure.AB
	if err := json.Unmarshal([]byte(out), &ab); err != nil {
		t.Fatalf("not an AB readout: %v\n%s", err, out)
	}
	if len(ab.Arms) != 2 || ab.Decidable {
		t.Errorf("readout = %+v, want two arms and not decidable", ab)
	}
}

func TestStatsAB_ExcludesTheOtherSections(t *testing.T) {
	repo := statsRepo(t, nil)
	for _, other := range []string{"--briefs", "--shadow"} {
		if code, _, errOut := runStatsCmd(t, "--repo", repo, "--ab", other); code != 2 || !strings.Contains(errOut, "--ab") {
			t.Errorf("--ab %s exit = %d, stderr %q, want 2 naming --ab", other, code, errOut)
		}
	}
}

// ariadne's lanes come from a plain `git worktree add` and record no lane-arm event;
// the read still puts each in its arm, from the repo's key and the lane's name.
func TestStatsAB_CountsALaneThatRecordedNoArm(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{
		time.Hour: {Kind: "commit_gate", Lane: "calc-split", Verdict: "green"},
	})
	code, out, errOut := runStatsCmd(t, "--repo", repo, "--ab", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	var ab measure.AB
	if err := json.Unmarshal([]byte(out), &ab); err != nil {
		t.Fatalf("not an AB readout: %v\n%s", err, out)
	}
	if n := ab.Arms[0].Lanes + ab.Arms[1].Lanes; n != 1 {
		t.Errorf("lanes in arms = %d, want 1 (calc-split, assigned at read time)", n)
	}
}
