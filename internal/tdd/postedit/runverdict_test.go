package postedit

import (
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// What the store is told of a finished phase: a failing run is red, a passing
// one green, and a phase that reached no verdict on the code is not tested,
// never green. A build that compiled is no verdict at all.
func TestPhaseVerdict_ReadsWhatThePhaseEstablished(t *testing.T) {
	for _, tc := range []struct {
		name     string
		phase    string
		out      PhaseOutcome
		log      string
		want     kernel.Verdict
		cause    string
		verdicts bool
	}{
		{"pass", "run", PhaseOutcome{}, "ok  \tx\t0.01s\n", kernel.VerdictGreen, "", true},
		{"fail", "run", PhaseOutcome{ExitCode: 1}, failingGoLog, kernel.VerdictRed, "", true},
		{"build that failed", "build", PhaseOutcome{ExitCode: 101}, "error[E0425]: cannot find value\n", kernel.VerdictRed, "", true},
		{"build that compiled", "build", PhaseOutcome{}, "", "", "", false},
		{"no slot", "run", PhaseOutcome{ExitCode: 125, SetupFailed: true}, "", kernel.VerdictNotTested, kernel.CauseInfra, true},
		{"cut short", "run", PhaseOutcome{ExitCode: 1, Inconclusive: "SKIPPED"}, "", kernel.VerdictNotTested, kernel.CauseSkipped, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := DeferredJob{Project: t.TempDir(), Phase: tc.phase, Log: filepath.Join(t.TempDir(), "run.log")}
			mustWrite(t, j.Log, tc.log)

			got, cause, ok := phaseVerdict(j, tc.out)

			if ok != tc.verdicts || got != tc.want || cause != tc.cause {
				t.Fatalf("verdict = %q/%q (%v), want %q/%q (%v)", got, cause, ok, tc.want, tc.cause, tc.verdicts)
			}
		})
	}
}

// The wrapper that runs the tests records their verdict on the tree it read
// before they started, and says so in the outcome the hook reads.
func TestRunPhase_RecordsTheRunsVerdictOnTheTreeItJudged(t *testing.T) {
	root, _ := laneAt(t, "ddd444")
	withIsolatedBuildLock(t)
	stubPhaseLint(t, nil)
	j := lintJob(t, root, writeMarkerCmd(filepath.Join(root, "ran")))

	RunPhase(writeJob(t, j))

	out, _ := deferredResult(j)
	if out.TreeKey != "ddd444" || out.StoreResult != kernel.VerdictGreen {
		t.Fatalf("outcome = %+v, want the green recorded on ddd444", out)
	}
	s, err := openRunStoreFn(root)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.ReadVerdict("ddd444"); !ok || len(v.Runs) != 1 || v.Runs[0].Unit != runUnit(root) {
		t.Fatalf("store verdict = %+v (%v), want the one run of %s", v, ok, runUnit(root))
	}
}
