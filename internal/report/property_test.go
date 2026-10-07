package report

import (
	"encoding/json"
	"testing"

	"pgregory.net/rapid"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A report is a fold of the log: the same events in any order are the same bytes.
func TestBuild_SameLogInAnyOrderIsTheSameBytes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		kinds := []string{"deny", "override", "commit_gate", "run.result", "escape", "shadow", "stage.timing"}
		verdicts := []string{"", "lint-blocked", "TIMEOUT", "ratchet-clean", "not-tested", "escape", "standdown-x:y"}
		n := rapid.IntRange(0, 25).Draw(rt, "n")
		evs := make([]tdd.Event, n)
		for i := range evs {
			evs[i] = evAt(int64(i+1), float64(rapid.IntRange(0, 9000).Draw(rt, "age")),
				rapid.SampledFrom(kinds).Draw(rt, "kind"), rapid.SampledFrom([]string{"lane/a", "lane/b"}).Draw(rt, "lane"),
				rapid.SampledFrom(verdicts).Draw(rt, "verdict"), "rule", rapid.SampledFrom([]string{"r1", "r2"}).Draw(rt, "rule"))
		}
		shuffled := rapid.Permutation(evs).Draw(rt, "perm")
		a, b := build(evs), build(shuffled)
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) || a.Text() != b.Text() {
			rt.Fatalf("order changed the report:\n%s\n%s", a.Text(), b.Text())
		}
	})
}
