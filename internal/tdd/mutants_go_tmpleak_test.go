package tdd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gremlins kills a mutant's test process outright on failfast or a timeout,
// so that process's own TestMain never reaches its deferred os.RemoveAll —
// exactly the pattern main_test.go's own TestMain uses for
// "aphrollo-tdd-pkgtest-*". measureEnv points TMPDIR at this lane's
// measurement area (never the OS temp dir), so every one of those orphaned
// directories lands there instead, and nothing sweeps them: the area grows by
// one leftover directory per killed mutant, forever, across every `gate
// mutants run`.
//
// This proves the growth across two separate runs of the SAME lane, which is
// the shape that actually fills a disk: a single run's own leftover would be
// unremarkable, but a measurement area that keeps every run's dead children
// on top of the last one does not.
func TestMeasureGoLane_SweepsAKilledMutantsOrphanedTempDirBetweenRuns(t *testing.T) {
	root, base := measurableTorqueLane(t)
	area := measureTempDir(root)
	noSurvivors := `{"files":[{"file_name":"torque/torque.go","mutations":[
		{"type":"ARITHMETIC_BASE","status":"KILLED","line":5,"column":16}]}]}`

	run := 0
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		run++
		tmp := envValueOf(c.Env, "TMPDIR")
		if tmp == "" {
			t.Fatalf("run %d: gremlins was given no TMPDIR", run)
		}
		// Stands in for a killed mutant's test binary: a directory made under
		// TMPDIR whose own cleanup never got the chance to run.
		if _, err := os.MkdirTemp(tmp, "aphrollo-tdd-pkgtest-"); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		mustWrite(t, gremlinsReportPath(root), noSurvivors)
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base}); err != nil {
		t.Fatalf("run 1: MeasureLane: %v", err)
	}
	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base}); err != nil {
		t.Fatalf("run 2: MeasureLane: %v", err)
	}

	entries, err := os.ReadDir(area)
	if err != nil {
		t.Fatalf("reading %s: %v", area, err)
	}
	var orphans []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "aphrollo-tdd-pkgtest-") {
			orphans = append(orphans, filepath.Join(area, e.Name()))
		}
	}
	if len(orphans) != 0 {
		t.Fatalf("area still holds %d killed-mutant temp dir(s) after two runs: %v — the area is never swept",
			len(orphans), orphans)
	}
}
