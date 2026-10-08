package costhistory

import (
	"reflect"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

func TestRuns_ReadsOnlySuiteCostEventsOldestFirst(t *testing.T) {
	events := []core.Event{
		{At: "2026-10-03T10:00:00.000Z", Kind: testcost.EventKind, Secs: 300, Detail: map[string]string{"t:m.B": "11"}},
		{At: "2026-10-01T10:00:00.000Z", Kind: "merge_gate", Secs: 5},
		{At: "2026-10-02T10:00:00.000Z", Kind: testcost.EventKind, Secs: 200, Detail: map[string]string{"p:m": "40", "t:m.A": "12"}},
		{At: "not a time", Kind: testcost.EventKind, Secs: 1},
	}
	got := Runs(events)
	want := []testcost.Run{
		{At: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC), Secs: 200, Tests: map[string]float64{"m.A": 12}, Pkgs: map[string]float64{"m": 40}},
		{At: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC), Secs: 300, Tests: map[string]float64{"m.B": 11}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, want %+v (other kinds and an unreadable time are not a cost record)", got, want)
	}
}

func TestRead_ReturnsWhatTheMergeGateRecordedForTheRepo(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := t.TempDir()
	core.AppendEvent(core.Event{Kind: testcost.EventKind, Root: root, Secs: 90, Detail: map[string]string{"t:m.A": "12.5"}})
	got := Read(root)
	if len(got) != 1 || got[0].Secs != 90 || got[0].Tests["m.A"] != 12.5 {
		t.Fatalf("read = %+v, want the one recorded run", got)
	}
}
