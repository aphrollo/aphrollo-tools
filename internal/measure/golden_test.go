package measure

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// golden decodes event lines as the writers recorded them, so a fold is held to
// the bytes on disk and not to a shape a test builder invented.
func golden(t *testing.T, lines string) []tdd.Event {
	t.Helper()
	var out []tdd.Event
	for line := range strings.SplitSeq(strings.TrimSpace(lines), "\n") {
		var e tdd.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("golden line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func computeGolden(events []tdd.Event) Report {
	return Compute(events, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), Options{})
}

// A PR's first run failed, nobody read it through a verb, and the green of the
// rerun is what `workspace merge` recorded: the first-run red is written with
// the run's own time, so it comes first.
func TestCI_ARedFirstRunRecordedAfterTheFactOutranksTheGreenRerun(t *testing.T) {
	events := golden(t, `
{"v":1,"at":"2026-10-03T19:43:32.330Z","lane":"lane/merge-judged-sha","kind":"pr_opened","verdict":"ok","detail":{"draft":"false","pr":"1186"}}
{"v":1,"at":"2026-10-03T20:17:55.000Z","lane":"lane/merge-judged-sha","kind":"ci","verdict":"green","detail":{"ci":"github","pr":"1186","sha":"4628333bb45183a3fda6754558c1bc84efca3156"}}
{"v":1,"at":"2026-10-03T19:43:33.000Z","lane":"lane/merge-judged-sha","kind":"ci","verdict":"red","detail":{"cause":"test","ci":"github","pr":"1186","sha":"ef2843462a9f69354fba613b57675d61642e670a"}}
`)
	got := computeGolden(events).CI
	if got.Lanes != 1 || got.Green != 0 || len(got.RedByCause) != 1 || got.RedByCause[0] != (Count{"test", 1}) {
		t.Fatalf("ci = %+v, want one lane, none green, red by test", got)
	}
}

// The merge queue ran the PR's checks on trunk's next state and removed it for
// failed_checks: the lane's first PR run was green, the lane still went red.
func TestCI_AQueueRunRedAfterAGreenPRRunMakesTheLaneRed(t *testing.T) {
	events := golden(t, `
{"v":1,"at":"2026-10-04T09:00:00.000Z","lane":"lane/q","kind":"ci","verdict":"green","detail":{"ci":"github","pr":"1200","sha":"aaaa"}}
{"v":1,"at":"2026-10-04T09:20:00.000Z","lane":"lane/q","kind":"ci","verdict":"red","detail":{"cause":"queue","ci":"queue","pr":"1200","sha":"bbbb"}}
{"v":1,"at":"2026-10-04T09:30:00.000Z","lane":"lane/ok","kind":"ci","verdict":"green","detail":{"ci":"github","pr":"1201","sha":"cccc"}}
`)
	got := computeGolden(events).CI
	if got.Lanes != 2 || got.Green != 1 || len(got.RedByCause) != 1 || got.RedByCause[0] != (Count{"queue", 1}) {
		t.Fatalf("ci = %+v, want 2 lanes, 1 green, red by queue", got)
	}
	if want := []OSRate{{"unknown", 2, 1}}; !reflect.DeepEqual(got.ByOS, want) {
		t.Fatalf("by os = %+v, want %+v", got.ByOS, want)
	}
}

// A queue red that is the only ci event of a lane (the PR run was never read)
// is the lane's red, and a second queue red of the same lane is not another.
func TestCI_AQueueRunRedIsCountedOncePerLane(t *testing.T) {
	events := golden(t, `
{"v":1,"at":"2026-10-04T09:20:00.000Z","lane":"lane/q","kind":"ci","verdict":"red","detail":{"cause":"queue","ci":"queue","pr":"1200","sha":"bbbb"}}
{"v":1,"at":"2026-10-04T09:50:00.000Z","lane":"lane/q","kind":"ci","verdict":"red","detail":{"cause":"queue","ci":"queue","pr":"1200","sha":"dddd"}}
`)
	got := computeGolden(events).CI
	if got.Lanes != 1 || got.Green != 0 || got.RedByCause[0].N != 1 {
		t.Fatalf("ci = %+v, want one red lane", got)
	}
}

// The bash hook once logged an allowed narrowed rerun as an override; it is not
// the waiver of a block, so it is neither an override nor a wrong block.
func TestDenies_AnAllowedNarrowedRerunIsNotAnOverrideOrAWrongBlock(t *testing.T) {
	events := golden(t, `
{"v":1,"at":"2026-10-03T23:30:00.000Z","lane":"lane/f20","kind":"deny","stage":"preedit","verdict":"pretooluse-denied:disabled-test","detail":{"rule":"disabled-test","cause":"smell"}}
{"v":1,"at":"2026-10-03T23:36:55.024Z","lane":"lane/f20","kind":"override","stage":"preedit","verdict":"override-bash-narrowed","detail":{"override":"override-bash-narrowed"}}
{"v":1,"at":"2026-10-03T23:37:20.365Z","lane":"lane/f20","kind":"override","stage":"preedit","verdict":"override-bash-narrowed","detail":{"override":"override-bash-narrowed"}}
`)
	got := computeGolden(events).Denies
	if got.Overrides != 0 || got.WrongBlocks != 0 || len(got.ByOverride) != 0 {
		t.Fatalf("denies = %+v, want no override and no wrong block", got)
	}
}

// One deny is one wrong block however many overrides follow it, and an
// override long after the deny is not one.
func TestDenies_AWrongBlockIsADenyNotAnOverride(t *testing.T) {
	events := golden(t, `
{"v":1,"at":"2026-10-03T10:00:00.000Z","lane":"lane/a","kind":"deny","verdict":"pretooluse-denied:primary-write","detail":{"rule":"primary-write"}}
{"v":1,"at":"2026-10-03T10:01:00.000Z","lane":"lane/a","kind":"override","verdict":"override-primary","detail":{"override":"override-primary"}}
{"v":1,"at":"2026-10-03T10:02:00.000Z","lane":"lane/a","kind":"override","verdict":"override-primary","detail":{"override":"override-primary"}}
{"v":1,"at":"2026-10-03T10:30:00.000Z","lane":"lane/a","kind":"override","verdict":"override-primary","detail":{"override":"override-primary"}}
`)
	got := computeGolden(events).Denies
	if got.Overrides != 3 || got.WrongBlocks != 1 {
		t.Fatalf("denies = %+v, want 3 overrides and 1 wrong block", got)
	}
}

// The rename of #1197: a narrowed rerun allowed beside an inconclusive verdict
// is logged as rerun-bash-narrowed, which is no override and no deny.
func TestRuns_ARenamedNarrowedRerunLineIsNeitherDenyNorOverride(t *testing.T) {
	events := golden(t, `
{"v":1,"at":"2026-10-04T10:00:00.000Z","lane":"lane/a","kind":"gate","stage":"preedit","verdict":"rerun-bash-narrowed"}
`)
	got := computeGolden(events)
	if got.Denies.Denies != 0 || got.Denies.Overrides != 0 {
		t.Fatalf("denies = %+v, want none", got.Denies)
	}
}

// A queue-merged PR nobody waited for: queued by the verb, merged later and
// recorded when local trunk took the merge in. Speed is open to that merge.
func TestSpeed_AQueueMergeRecordedWhenTrunkTookItInClosesTheLane(t *testing.T) {
	events := golden(t, `
{"v":1,"at":"2026-10-04T09:00:00.000Z","lane":"lane/q","kind":"edit"}
{"v":1,"at":"2026-10-04T09:10:00.000Z","lane":"lane/q","kind":"merge","verdict":"queued","detail":{"method":"merge queue","pr":"1200"}}
{"v":1,"at":"2026-10-04T10:00:00.000Z","lane":"lane/q","kind":"merge","verdict":"ok","detail":{"method":"merge queue","pr":"1200","sha":"eeee"}}
`)
	got := computeGolden(events).Speed
	if got.N != 1 || got.P50 != 3600 {
		t.Fatalf("speed = %+v, want one lane of 3600s", got)
	}
}
