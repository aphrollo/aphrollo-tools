package shadow

import (
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/store"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// green is a green run of runUnit that ended at end after running for ms.
func runOf(result kernel.Verdict, runUnit string, end time.Time, ms int64) store.RunVerdict {
	return store.RunVerdict{Runner: "go", Unit: runUnit, Result: result, MS: ms, At: end}
}

func TestCovered_IsAGreenRunOfTheUnitOnATreeHoldingItsNewestEdit(t *testing.T) {
	lane := Unit{ID: "internal/lane", Project: ".", Pkg: "internal/lane", Kind: unitGoPackage}
	unitOf := func(file string) (Unit, bool) {
		switch file {
		case "lane.go":
			return lane, true
		case "store.go":
			return Unit{ID: "internal/store", Project: ".", Pkg: "internal/store", Kind: unitGoPackage}, true
		}
		return Unit{}, false
	}
	edit := func(file string, at time.Time) LedgerEdit { return LedgerEdit{File: file, At: at} }
	cases := []struct {
		name  string
		runs  []store.RunVerdict
		edits []LedgerEdit
		want  bool
	}{
		{"a green that started after the edit covers it",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./internal/lane/...", t0.Add(10*time.Second), 4000)},
			[]LedgerEdit{edit("lane.go", t0)}, true},
		{"a green that started before the edit does not hold it",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./internal/lane/...", t0.Add(2*time.Second), 4000)},
			[]LedgerEdit{edit("lane.go", t0)}, false},
		{"no run yet is unknown, which is uncovered",
			nil, []LedgerEdit{edit("lane.go", t0)}, false},
		{"a red run is no cover",
			[]store.RunVerdict{runOf(kernel.VerdictRed, ".|go test ./...", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0)}, false},
		{"a run that did not test is no cover",
			[]store.RunVerdict{runOf(kernel.VerdictNotTested, ".|go test ./...", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0)}, false},
		{"a green of another package does not cover the unit",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./internal/store", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0)}, false},
		{"a green of another project does not cover the unit",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, "tools/x|go test ./...", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0)}, false},
		{"a whole-module run covers every package",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./...", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0)}, true},
		{"a run naming no package ran the whole project",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0)}, true},
		{"only the newest edit of the unit counts, and another unit's edit is no edit of it",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./...", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0), edit("store.go", t0.Add(time.Minute))}, true},
		{"an edit after the green leaves the unit uncovered again",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./...", t0.Add(10*time.Second), 1000)},
			[]LedgerEdit{edit("lane.go", t0), edit("lane.go", t0.Add(time.Minute))}, false},
		{"a unit with no edit on this head is covered by its last green",
			[]store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./internal/lane", t0, 1000)},
			nil, true},
	}
	for _, c := range cases {
		if got := Covered(lane, c.runs, c.edits, unitOf); got != c.want {
			t.Errorf("%s: Covered = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCovered_AProjectRootUnitIsCoveredByAnyGreenRunOfItsProject(t *testing.T) {
	crate := Unit{ID: "rust:crates/engine", Project: "crates/engine", Kind: unitProjectRoot}
	unitOf := func(string) (Unit, bool) { return crate, true }
	runs := []store.RunVerdict{runOf(kernel.VerdictGreen, "crates/engine|cargo test -p engine", t0.Add(5*time.Second), 1000)}
	if !Covered(crate, runs, []LedgerEdit{{File: "lib.rs", At: t0}}, unitOf) {
		t.Error("a green cargo run of the crate does not cover its unit")
	}
	if Covered(crate, []store.RunVerdict{runOf(kernel.VerdictGreen, "crates/other|cargo test", t0.Add(5*time.Second), 1000)}, nil, unitOf) {
		t.Error("a green run of another crate covers the unit")
	}
}
