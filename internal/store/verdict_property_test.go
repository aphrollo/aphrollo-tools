package store

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"pgregory.net/rapid"
)

var (
	propKeys    = []string{keyA, keyB, "ab12"} // the last is a short key, valid too
	propRunners = []string{"go-test", "ci"}
	propUnits   = []string{"pkg/a", "pkg/b"}
	propResults = []kernel.Verdict{kernel.VerdictGreen, kernel.VerdictRed, kernel.VerdictRedBogus, kernel.VerdictNotTested}
	propLaws    = []string{"secrets", "module_size"}
	propLawRes  = []string{LawPass, LawRefuse, LawUnmeasured}
	propConc    = []string{"success", "failure"}
)

func drawAdd(t *rapid.T) Verdict {
	var add Verdict
	for range rapid.IntRange(0, 3).Draw(t, "runs") {
		add.Runs = append(add.Runs, RunVerdict{
			Runner: rapid.SampledFrom(propRunners).Draw(t, "runner"), Unit: rapid.SampledFrom(propUnits).Draw(t, "unit"),
			Result: rapid.SampledFrom(propResults).Draw(t, "result"),
			Test:   rapid.SampledFrom([]string{"TestX", "TestY"}).Draw(t, "test"), MS: rapid.Int64Range(0, 9).Draw(t, "ms"),
		})
	}
	for range rapid.IntRange(0, 2).Draw(t, "laws") {
		add.Laws = append(add.Laws, LawVerdict{Law: rapid.SampledFrom(propLaws).Draw(t, "law"), Result: rapid.SampledFrom(propLawRes).Draw(t, "lawres")})
	}
	for range rapid.IntRange(0, 2).Draw(t, "ci") {
		add.CI = append(add.CI, CIVerdict{
			OS: rapid.SampledFrom([]string{"linux", "windows"}).Draw(t, "os"), Run: rapid.SampledFrom([]string{"1", "2"}).Draw(t, "run"),
			Conclusion: rapid.SampledFrom(propConc).Draw(t, "conclusion"),
		})
	}
	return add
}

// TestRecordVerdict_noRecordedResultIsEverLost holds the written-once rule over
// any history of writes to a few keys, through stores opened afresh at random
// the way separate processes see the directory: every result any writer ever
// recorded for a key is in the file, nothing is in it that no writer recorded,
// and no result occurs twice. The model is three plain sets.
func TestRecordVerdict_noRecordedResultIsEverLost(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		s := open(t, dir)
		type model struct {
			runs map[runID]bool
			laws map[lawID]bool
			ci   map[CIVerdict]bool
		}
		want := map[string]*model{}
		steps := rapid.IntRange(1, 30).Draw(rt, "steps")
		for i := range steps {
			if rapid.IntRange(0, 4).Draw(rt, "reopen") == 0 {
				s = open(t, dir)
			}
			key := rapid.SampledFrom(propKeys).Draw(rt, "key")
			add := drawAdd(rt)
			_, err := s.RecordVerdict(bounded(t), key, "", add)
			if err != nil {
				rt.Fatalf("step %d: RecordVerdict(%s) = %v", i, key, err)
			}
			m := want[key]
			if m == nil {
				m = &model{map[runID]bool{}, map[lawID]bool{}, map[CIVerdict]bool{}}
				want[key] = m
			}
			for _, r := range add.Runs {
				m.runs[runID{r.Runner, r.Unit, r.Result, r.Cause}] = true
			}
			for _, l := range add.Laws {
				m.laws[lawID{l.Law, l.Result}] = true
			}
			for _, c := range add.CI {
				m.ci[c] = true
			}
			for k, m := range want {
				got, _ := read(t, s, k)
				gotRuns, gotLaws, gotCI := map[runID]int{}, map[lawID]int{}, map[CIVerdict]int{}
				for _, r := range got.Runs {
					gotRuns[runID{r.Runner, r.Unit, r.Result, r.Cause}]++
				}
				for _, l := range got.Laws {
					gotLaws[lawID{l.Law, l.Result}]++
				}
				for _, c := range got.CI {
					gotCI[c]++
				}
				if len(gotRuns) != len(m.runs) || len(gotLaws) != len(m.laws) || len(gotCI) != len(m.ci) ||
					len(got.Runs) != len(m.runs) || len(got.Laws) != len(m.laws) || len(got.CI) != len(m.ci) {
					rt.Fatalf("step %d, key %s: file holds %d runs, %d laws, %d CI (%d, %d, %d distinct); the writers recorded %d, %d, %d",
						i, k, len(got.Runs), len(got.Laws), len(got.CI), len(gotRuns), len(gotLaws), len(gotCI), len(m.runs), len(m.laws), len(m.ci))
				}
				for id := range m.runs {
					if gotRuns[id] != 1 {
						rt.Fatalf("step %d, key %s: run %+v recorded but found %d times", i, k, id, gotRuns[id])
					}
				}
				for id := range m.laws {
					if gotLaws[id] != 1 {
						rt.Fatalf("step %d, key %s: law %+v recorded but found %d times", i, k, id, gotLaws[id])
					}
				}
				for id := range m.ci {
					if gotCI[id] != 1 {
						rt.Fatalf("step %d, key %s: CI %+v recorded but found %d times", i, k, id, gotCI[id])
					}
				}
			}
		}
	})
}
