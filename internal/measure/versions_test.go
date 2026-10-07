package measure

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func versionsTagged(ver string, evs ...tdd.Event) []tdd.Event {
	for i := range evs {
		evs[i].BinVer = ver
	}
	return evs
}

func TestVersions_ListsEachBinaryVersionTheWindowSpans(t *testing.T) {
	events := abCat(
		versionsTagged("1.0.0", ev(10, "lane/a", "edit", nil)),
		versionsTagged("1.1.0", ev(20, "lane/b", "edit", nil), ev(21, "lane/b", "edit", nil)),
		[]tdd.Event{ev(30, "lane/c", "edit", nil)}, // written before events carried a version
	)
	got := Versions(events, base.Add(40*24*time.Hour), Options{})
	want := "1.0.0=1 1.1.0=2 unknown=1"
	var parts []string
	for _, v := range got {
		parts = append(parts, v.Version+"="+strconv.Itoa(v.Events))
	}
	if strings.Join(parts, " ") != want {
		t.Fatalf("Versions = %v, want %s", got, want)
	}
}

func TestVersions_OnlyCountsTheWindow(t *testing.T) {
	events := abCat(
		versionsTagged("0.9.0", ev(0, "lane/a", "edit", nil)),
		versionsTagged("1.0.0", ev(39*24*3600, "lane/a", "edit", nil)),
	)
	got := Versions(events, base.Add(40*24*time.Hour), Options{Window: 5 * 24 * time.Hour})
	if len(got) != 1 || got[0].Version != "1.0.0" {
		t.Fatalf("Versions = %v, want only 1.0.0 inside the last 5 days", got)
	}
}

// A lane that straddles an update stays whole in one slice, under the version
// that opened it: its arm event and its decisions must not be split.
func TestSplitByVersion_KeepsALanesEventsTogetherUnderTheVersionThatOpenedIt(t *testing.T) {
	old := versionsTagged("1.0.0", ev(10, "lane/a", "lane-arm", detail("arm", "enforce", "why", "assigned")))
	later := versionsTagged("1.1.0", ev(20, "lane/a", "edit", nil), ev(30, "lane/b", "edit", nil))
	got := SplitByVersion(abCat(old, later))
	if len(got) != 2 || got[0].Version != "1.0.0" || len(got[0].Events) != 2 || got[1].Version != "1.1.0" || len(got[1].Events) != 1 {
		t.Fatalf("SplitByVersion = %+v; want 1.0.0 with lane/a's two events, 1.1.0 with lane/b's one", got)
	}
}

func TestComputeAB_NotesAnArmWhoseLanesRanUnderDifferentVersions(t *testing.T) {
	events := abCat(
		versionsTagged("1.0.0", abLane(0, "lane/e1", "enforce", "go", "block")...),
		versionsTagged("1.1.0", abLane(0, "lane/e2", "enforce", "go", "block")...),
		versionsTagged("1.1.0", abLane(0, "lane/w1", "warn", "go", "warn")...),
		versionsTagged("1.1.0", abLane(0, "lane/w2", "warn", "go", "warn")...),
	)
	ab := computeAB(events, Options{})
	if got := strings.Join(abRow(t, ab, "enforce").Versions, ","); got != "1.0.0,1.1.0" {
		t.Fatalf("enforce versions = %q, want 1.0.0,1.1.0", got)
	}
	if v := abRow(t, ab, "warn").Versions; v != nil {
		t.Fatalf("warn versions = %v, want none listed for a single version", v)
	}
	text := ab.Text()
	if !strings.Contains(text, "enforce lanes ran under different versions: 1.0.0, 1.1.0") {
		t.Fatalf("the readout does not note the mix:\n%s", text)
	}
	if strings.Contains(text, "warn lanes ran under different") {
		t.Fatalf("the readout notes a mix on the single-version arm:\n%s", text)
	}
}

func TestVersionsText_NamesTheVersionsAWindowSpans(t *testing.T) {
	got := VersionsText([]VersionCount{{"1.0.0", 3}, {"unknown", 1}})
	if want := "versions in this window: 1.0.0 (3 events), unknown (1 event)\n"; got != want {
		t.Fatalf("VersionsText = %q, want %q", got, want)
	}
	if VersionsText(nil) != "" {
		t.Fatal("an empty window names no versions")
	}
}
