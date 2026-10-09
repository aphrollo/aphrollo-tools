package report

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A lane that recorded no lane-arm event is in the report's A/B, window and whole log,
// once the report is given the repo's key.
func TestBuild_TheABCountsALaneThatRecordedNoArm(t *testing.T) {
	old := evAt(1, 60, "commit_gate", "calc-split", "green")
	old.BinVer = "1.40.0"
	start := evAt(2, 120, "lane-arm", "starter", "", "why", "pinned", "mode", "enforce")
	start.BinVer = "1.40.0"
	evs := []tdd.Event{start, old}
	r := Build(Input{Events: evs, Now: now, Window: week, Repo: "r", RepoKey: "example.com/org/repo"})
	for name, ab := range map[string][]int{"window": {r.AB.Arms[0].Lanes, r.AB.Arms[1].Lanes}, "total": {r.ABTotal.Arms[0].Lanes, r.ABTotal.Arms[1].Lanes}} {
		if ab[0]+ab[1] != 1 {
			t.Errorf("%s A/B lanes = %v, want one lane in one arm", name, ab)
		}
	}
}
