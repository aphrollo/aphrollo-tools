package report

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/expect"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func withVer(e tdd.Event, ver string) tdd.Event {
	e.BinVer = ver
	return e
}

// expectFixture: PR 1 expects edit-suite p50 down (it fell), PR 2 commit-gate-total
// p50 down (it rose), PR 3 merge-gate-total p50 down (no clear change), PR 4
// ci-pipeline p50 down on a release only 5 lanes ran on. Before is v1.0.0,
// after is v1.1.0 (30 lanes) and v1.2.0 (5 lanes).
func expectFixture() []tdd.Event {
	var evs []tdd.Event
	seq := int64(0)
	add := func(e tdd.Event) { seq++; e.Seq = seq; evs = append(evs, e) }
	for i := 1; i <= 10; i++ {
		lane := fmt.Sprintf("old-%d", i)
		age := float64(5000 - i)
		add(withVer(timed(evAt(0, age, "stage.timing", lane, "green"), "postedit", "go test", float64(100+i)), "v1.0.0"))
		add(withVer(timed(evAt(0, age, "commit_gate_result", lane, "green"), "", "", float64(10+i)), "v1.0.0"))
		add(withVer(timed(evAt(0, age, "merge_gate_result", lane, "green"), "", "", float64(i)), "v1.0.0"))
	}
	for i := 1; i <= 30; i++ {
		lane := fmt.Sprintf("new-%d", i)
		age := float64(3000 - i)
		add(withVer(timed(evAt(0, age, "stage.timing", lane, "green"), "postedit", "go test", float64(10+i)), "v1.1.0"))
		add(withVer(timed(evAt(0, age, "commit_gate_result", lane, "green"), "", "", float64(100+i)), "v1.1.0"))
		add(withVer(timed(evAt(0, age, "merge_gate_result", lane, "green"), "", "", float64((i-1)%10+1)), "v1.1.0"))
	}
	for i := 1; i <= 5; i++ {
		lane := fmt.Sprintf("newer-%d", i)
		add(withVer(timed(evAt(0, float64(1000-i), "stage.timing", lane, "green"), "postedit", "go test", 5), "v1.2.0"))
	}
	merge := func(pr, line, after string) {
		add(withVer(evAt(0, 4000, "merge", "merger", "ok", "pr", pr, "expect", line, "after", after), "v1.0.0"))
	}
	merge("1", "edit-suite p50 down", "1.0.0")
	merge("2", "commit-gate-total p50 down", "1.0.0")
	merge("3", "merge-gate-total p50 down", "1.0.0")
	merge("4", "ci-pipeline p50 down", "1.1.0")
	return evs
}

func expectSection(text string) string {
	_, rest, _ := strings.Cut(text, "Expectations (")
	section, _, _ := strings.Cut(rest, "\n\n")
	return "Expectations (" + section
}

func TestExpectations_ReadHeldNotHeldNoChangeAndPendingFromFixtureEvents(t *testing.T) {
	got := expectSection(build(expectFixture()).Text())
	want := `Expectations (a merged PR's expect: lines, read once 30 lanes ran on a binary newer than its release; ~ is no clear change)
  PR #1 edit-suite p50 down: held: 1.8m -> 23s (n 10 -> 35, p <0.001)
  PR #2 commit-gate-total p50 down: not held: 15s -> 1.9m, the other way (n 10 -> 30, p <0.001)
  PR #3 merge-gate-total p50 down: not held: 5s -> 5s ~ no clear change (n 10 -> 30, p 1.000)
  PR #4 ci-pipeline p50 down: pending (5 of 30 lanes)`
	if got != want {
		t.Errorf("section =\n%s\nwant\n%s", got, want)
	}
}

func TestExpectations_ANotHeldOneIsAProposalAndAHeldOneIsNot(t *testing.T) {
	var rules []string
	for _, p := range build(expectFixture()).Proposals {
		if strings.HasPrefix(p.Rule, "expect: ") {
			rules = append(rules, p.Rule)
		}
	}
	want := []string{"expect: PR #2 commit-gate-total p50 down", "expect: PR #3 merge-gate-total p50 down"}
	if !slices.Equal(rules, want) {
		t.Errorf("expect proposals = %v, want %v", rules, want)
	}
}

func TestExpectations_AreTheSameBytesWhateverTheEventOrder(t *testing.T) {
	evs := expectFixture()
	first := build(evs).Text()
	slices.Reverse(evs)
	if second := build(evs).Text(); second != first {
		t.Errorf("reversed input changed the report:\n%s\nvs\n%s", second, first)
	}
}

func TestExpectations_ASmallSampleIsUnprovenNotHeld(t *testing.T) {
	var evs []tdd.Event
	for i := 1; i <= 30; i++ {
		evs = append(evs, withVer(evAt(int64(i), 3000-float64(i), "run.result", fmt.Sprintf("n-%d", i), "green"), "v1.1.0"))
	}
	evs = append(evs, withVer(timed(evAt(40, 3500, "stage.timing", "o", "green"), "postedit", "go test", 9), "v1.0.0"),
		withVer(evAt(41, 4000, "merge", "m", "ok", "pr", "9", "expect", "edit-suite p50 down", "after", "1.0.0"), "v1.0.0"))
	got := expectSection(build(evs).Text())
	if !strings.Contains(got, "PR #9 edit-suite p50 down: unproven: n too small (before 1, after 0 runs; 4 needed on each side)") {
		t.Errorf("section =\n%s", got)
	}
}

func TestExpectations_TheWrongBlockRateIsReadFromDeniesAndOverrides(t *testing.T) {
	var evs []tdd.Event
	seq := int64(100)
	add := func(e tdd.Event) { seq++; e.Seq = seq; evs = append(evs, e) }
	// before (v1.0.0): 4 denies, 4 waived; after (v1.1.0): 4 denies, 0 waived, 30 lanes.
	for i := 1; i <= 4; i++ {
		lane := fmt.Sprintf("o%d", i)
		add(withVer(evAt(0, 5000-float64(i)*10, "deny", lane, "", "rule", "r"), "v1.0.0"))
		add(withVer(evAt(0, 5000-float64(i)*10-1, "override", lane, "x", "override", "r"), "v1.0.0"))
	}
	for i := 1; i <= 30; i++ {
		lane := fmt.Sprintf("n%d", i)
		add(withVer(evAt(0, 3000-float64(i), "run.result", lane, "green"), "v1.1.0"))
		if i <= 4 {
			add(withVer(evAt(0, 3000-float64(i), "deny", lane, "", "rule", "r"), "v1.1.0"))
		}
	}
	add(withVer(evAt(0, 6000, "merge", "m", "ok", "pr", "7", "expect", "wrong-blocks rate down", "after", "1.0.0"), "v1.0.0"))
	got := expectSection(build(evs).Text())
	if !strings.Contains(got, "PR #7 wrong-blocks rate down: held: 100.0% -> 0.0% (n 4 -> 4)") {
		t.Errorf("section =\n%s", got)
	}
}

func TestExpectMetrics_EverySpeedMetricNamesARealSpeedClass(t *testing.T) {
	for _, m := range expect.Metrics {
		if m.Source == expect.SourceWrongBlocks {
			continue
		}
		if !slices.ContainsFunc(speedClasses, func(c speedClass) bool { return c.name == m.Source }) {
			t.Errorf("metric %s reads %q, which is no speed class", m.Name, m.Source)
		}
	}
}
