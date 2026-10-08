package report

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ver is e written by the binary version v.
func ver(e tdd.Event, v string) tdd.Event { e.BinVer = v; return e }

func runsOf(start int64, ageMin float64, v string, secs ...float64) []tdd.Event {
	var out []tdd.Event
	for i, s := range secs {
		out = append(out, ver(timed(evAt(start+int64(i), ageMin-float64(i), "stage.timing", "l", "green"), "postedit", "", s), v))
	}
	return out
}

func TestSpeed_AChangeIsClearOnlyWithFourRunsAndPUnderOneTwentieth(t *testing.T) {
	before := 8.0 * 24 * 60
	evs := append(runsOf(1, before, "", 10, 11, 12, 13), runsOf(10, 60, "", 1, 2, 3, 4)...)
	row := speedRow(t, build(evs), "edit suite")
	if !row.Clear || row.Change == nil || *row.Change != -9 || row.P < 0.028 || row.P > 0.029 {
		t.Errorf("4 runs apart on each side = %+v, want clear, change -9, p 2/70", row)
	}
	evs = append(runsOf(1, before, "", 10, 11, 12), runsOf(10, 60, "", 1, 2, 3, 4)...)
	row = speedRow(t, build(evs), "edit suite")
	if row.Clear || row.Change == nil {
		t.Errorf("3 runs before = %+v, want an unclear change still carrying its p50 difference", row)
	}
}

func TestSpeed_VersionsAreColumnsLabeledBySinceAndFirstDate(t *testing.T) {
	evs := append(runsOf(1, 2880, "1.0.0", 10, 11, 12, 13), runsOf(10, 600, "1.1.0", 1, 2, 3, 4)...)
	row := speedRow(t, build(evs), "edit suite")
	if len(row.Versions) != 2 {
		t.Fatalf("versions = %+v, want 2", row.Versions)
	}
	a, b := row.Versions[0], row.Versions[1]
	if a.Label != "since v1.0.0, 2026-10-05" || b.Label != "since v1.1.0, 2026-10-07" {
		t.Errorf("labels = %q, %q", a.Label, b.Label)
	}
	if a.N != 4 || a.P50 != 11 || b.P50 != 2 {
		t.Errorf("versions = %+v %+v", a, b)
	}
	if a.Change != nil || b.Change == nil || *b.Change != -9 || !b.Clear {
		t.Errorf("the second version against the first = %+v, want clear -9 (the first has nothing before it)", b)
	}
}

func TestSpeed_AVersionWithFewerThanFourRunsMergesIntoTheVersionBeforeAndSaysSo(t *testing.T) {
	evs := append(runsOf(1, 2880, "1.0.0", 10, 11, 12, 13), runsOf(10, 600, "1.1.0", 1, 2)...)
	evs = append(evs, runsOf(20, 300, "1.2.0", 3, 4, 5, 6)...)
	row := speedRow(t, build(evs), "edit suite")
	if len(row.Versions) != 2 {
		t.Fatalf("versions = %+v, want the 1.1.0 runs folded into 1.0.0 and 1.2.0 apart", row.Versions)
	}
	if got := row.Versions[0]; got.N != 6 || got.Label != "since v1.0.0, 2026-10-05 (merged with v1.1.0)" {
		t.Errorf("merged version = %+v", got)
	}
	if got := row.Versions[1]; got.Label != "since v1.2.0, 2026-10-07" {
		t.Errorf("last version = %+v", got)
	}
}

func TestSpeed_RunsWithNoVersionAreInTheRowButInNoVersionColumn(t *testing.T) {
	row := speedRow(t, build(runsOf(1, 60, "", 1, 2, 3, 4)), "edit suite")
	if row.N != 4 || len(row.Versions) != 0 {
		t.Errorf("row = %+v, want 4 runs and no version split", row)
	}
}

func changedFixture() Report {
	slower, faster := 5.0, -6.0
	return Report{
		Window:      "last 7d",
		Previous:    &Previous{Window: "last 7d", NotTested: 3, Waived: 1, Gone: []RuleCount{{Rule: "old-rule", N: 2}}},
		Friction:    []Friction{{Rule: "new-rule", Denies: 2, NotTested: 9}, {Rule: "kept-rule", Denies: 1, Prev: 1}},
		WrongBlocks: []WrongBlock{{Rule: "w", Denies: 4, Waived: 3}},
		Speed: Speed{Rows: []SpeedRow{
			{Stage: "edit suite", N: 10, P50: 4, PrevN: 10, PrevP50: 10, Change: &faster, Clear: true, P: 0.01},
			{Stage: "commit gate: go test", N: 14, P50: 95, PrevN: 9, PrevP50: 90, Change: &slower, Clear: true, P: 0.03},
			{Stage: "merge gate: x", N: 5, P50: 9, PrevN: 5, PrevP50: 5, Change: &slower},
		}},
	}
}

func TestChanges_ListsOnlyClearSpeedChangesSlowerFirstThenNewAndGoneRulesAndRisingCounts(t *testing.T) {
	got := strings.Join(changedFixture().Changes(), "\n")
	want := strings.Join([]string{
		"slower: commit gate: go test p50 1.5m -> 1.6m (+5s, p 0.03, 14 runs against 9)",
		"faster: edit suite p50 10s -> 4s (-6s, p 0.01, 10 runs against 10)",
		"new rule: new-rule (11)",
		"gone: old-rule (was 2)",
		"not-tested runs up: 3 -> 9",
		"wrong blocks up: 1 -> 3",
	}, "\n")
	if got != want {
		t.Errorf("changes:\n%s\nwant:\n%s", got, want)
	}
}

func TestChanges_NothingPassingIsOneLineNamingTheWindowBefore(t *testing.T) {
	r := Report{Window: "last 7d", Previous: &Previous{Window: "last 7d"}}
	if got := r.Changes(); len(got) != 1 || got[0] != "no clear change against the previous 7d" {
		t.Errorf("changes = %q", got)
	}
	if got := (Report{Window: "whole log"}).Changes(); len(got) != 1 || got[0] != "no window before to compare against" {
		t.Errorf("whole log changes = %q", got)
	}
}

func TestText_LeadsWithTheChangedBlockAboveTheSpeedBlock(t *testing.T) {
	r := changedFixture()
	r.Title, r.Repo = "T", "r"
	text := r.Text()
	c, s := strings.Index(text, "Changed"), strings.Index(text, "Speed (")
	if c < 0 || s < 0 || c > s || !strings.Contains(text[c:s], "slower: commit gate") {
		t.Errorf("the Changed block does not lead:\n%s", text)
	}
}
