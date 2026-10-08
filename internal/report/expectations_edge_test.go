package report

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/expect"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// edgeEvents has beforeN timed runs of v1.0.0, afterLanes lanes of v1.1.0 of
// which the first afterN carry a timed run, and PR 1 expecting edit-suite p50 down.
func edgeEvents(beforeN, afterLanes, afterN int) []tdd.Event {
	var evs []tdd.Event
	add := func(e tdd.Event) { e.Seq = int64(len(evs) + 1); evs = append(evs, e) }
	for i := 1; i <= beforeN; i++ {
		add(withVer(timed(evAt(0, 5000-float64(i), "stage.timing", fmt.Sprintf("b%d", i), "green"), "postedit", "go test", float64(100+i)), "v1.0.0"))
	}
	for i := 1; i <= afterLanes; i++ {
		lane := fmt.Sprintf("a%d", i)
		if i <= afterN {
			add(withVer(timed(evAt(0, 3000-float64(i), "stage.timing", lane, "green"), "postedit", "go test", float64(10+i)), "v1.1.0"))
		} else {
			add(withVer(evAt(0, 3000-float64(i), "run.result", lane, "green"), "v1.1.0"))
		}
	}
	add(withVer(evAt(0, 4000, "merge", "m", "ok", "pr", "1", "expect", "edit-suite p50 down", "after", "1.0.0"), "v1.0.0"))
	return evs
}

func edgeRow(t *testing.T, evs []tdd.Event) ExpectationRow {
	t.Helper()
	rows := build(evs).Expectations.Rows
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	return rows[0]
}

// The merge lane "m" is a v1.0.0 lane, so only the v1.1.0 lanes count as after.
func TestExpectations_TwentyNineLanesArePendingAndThirtyAreJudged(t *testing.T) {
	if r := edgeRow(t, edgeEvents(4, 29, 29)); r.State != expectPending || r.Lanes != 29 {
		t.Errorf("29 lanes = %+v, want pending", r)
	}
	if r := edgeRow(t, edgeEvents(4, 30, 30)); r.State == expectPending || r.Lanes != 30 {
		t.Errorf("30 lanes = %+v, want judged", r)
	}
}

func TestExpectations_ASampleOfThreeIsUnprovenAndFourIsTestedOnEachSide(t *testing.T) {
	cases := []struct {
		before, after int
		unproven      bool
	}{{3, 30, true}, {4, 3, true}, {4, 4, false}, {4, 30, false}}
	for _, c := range cases {
		r := edgeRow(t, edgeEvents(c.before, 30, c.after))
		if got := r.State == expectUnproven; got != c.unproven {
			t.Errorf("before %d, after %d: state %q, unproven want %v", c.before, c.after, r.State, c.unproven)
		}
		if !c.unproven && r.BeforeN != c.before {
			t.Errorf("before %d, after %d: tested n %d -> %d", c.before, c.after, r.BeforeN, r.AfterN)
		}
	}
}

func TestExpectations_ASampleOfAnUnknownClassIsIgnoredNotIndexed(t *testing.T) {
	evs := stampedOf(edgeEvents(4, 30, 30))
	rec := expectRecord{pr: "1", e: expect.Expectation{Metric: "edit-suite", Stat: "p50", Direction: "down"}, after: "1.0.0"}
	samples := []speedSample{{key: "no such class", secs: 1, ver: "v1.1.0"}}
	r := readExpectation(rec, evs, samples)
	if r.State != expectUnproven || r.BeforeN != 0 || r.AfterN != 0 {
		t.Errorf("row = %+v, want unproven with no sample read", r)
	}
}

func stampedOf(evs []tdd.Event) []stamped { return sortEvents(evs) }

func TestExpectations_APRWithNoNumberIsNamedBySeq(t *testing.T) {
	evs := edgeEvents(4, 30, 30)
	last := &evs[len(evs)-1]
	delete(last.Detail, "pr")
	if got := edgeRow(t, evs).PR; got != fmt.Sprintf("seq%d", last.Seq) {
		t.Errorf("PR = %q, want the merge event's seq", got)
	}
}

func TestExpectations_AnAfterRateIsDeniesWaivedOverDenies(t *testing.T) {
	var evs []tdd.Event
	add := func(e tdd.Event) { e.Seq = int64(len(evs) + 1); evs = append(evs, e) }
	for i := 1; i <= 4; i++ {
		lane := fmt.Sprintf("o%d", i)
		add(withVer(evAt(0, 5000-float64(i)*10, "deny", lane, "", "rule", "r"), "v1.0.0"))
		add(withVer(evAt(0, 5000-float64(i)*10-1, "override", lane, "x", "override", "r"), "v1.0.0"))
	}
	for i := 1; i <= 30; i++ {
		lane := fmt.Sprintf("n%d", i)
		add(withVer(evAt(0, 3000-float64(i)*10, "run.result", lane, "green"), "v1.1.0"))
		if i <= 4 {
			add(withVer(evAt(0, 3000-float64(i)*10, "deny", lane, "", "rule", "r"), "v1.1.0"))
		}
		if i <= 2 {
			add(withVer(evAt(0, 3000-float64(i)*10-1, "override", lane, "x", "override", "r"), "v1.1.0"))
		}
	}
	add(withVer(evAt(0, 6000, "merge", "m", "ok", "pr", "7", "expect", "wrong-blocks rate down", "after", "1.0.0"), "v1.0.0"))
	if got := edgeRow(t, evs).Description; !strings.Contains(got, "held: 100.0% -> 50.0% (n 4 -> 4)") {
		t.Errorf("description = %q", got)
	}
}

func TestExpectations_ThreeDeniesOnASideAreUnprovenAndFourAreRead(t *testing.T) {
	rate := func(beforeDenies, afterDenies int) ExpectationRow {
		var evs []tdd.Event
		add := func(e tdd.Event) { e.Seq = int64(len(evs) + 1); evs = append(evs, e) }
		for i := 1; i <= beforeDenies; i++ {
			add(withVer(evAt(0, 5000-float64(i)*10, "deny", fmt.Sprintf("o%d", i), "", "rule", "r"), "v1.0.0"))
		}
		for i := 1; i <= 30; i++ {
			lane := fmt.Sprintf("n%d", i)
			add(withVer(evAt(0, 3000-float64(i)*10, "run.result", lane, "green"), "v1.1.0"))
			if i <= afterDenies {
				add(withVer(evAt(0, 3000-float64(i)*10, "deny", lane, "", "rule", "r"), "v1.1.0"))
			}
		}
		add(withVer(evAt(0, 6000, "merge", "m", "ok", "pr", "7", "expect", "wrong-blocks rate down", "after", "1.0.0"), "v1.0.0"))
		return edgeRow(t, evs)
	}
	for _, c := range []struct {
		b, a     int
		unproven bool
	}{{3, 4, true}, {4, 3, true}, {4, 4, false}} {
		if got := rate(c.b, c.a).State == expectUnproven; got != c.unproven {
			t.Errorf("denies %d -> %d: unproven = %v, want %v", c.b, c.a, got, c.unproven)
		}
	}
}

func TestPText_RoundsToThreePlacesAndNamesAnythingBelowHalfAThousandth(t *testing.T) {
	if got := pText(0.0004); got != "<0.001" {
		t.Errorf("pText(0.0004) = %q", got)
	}
	if got := pText(0.0005); got != "0.001" {
		t.Errorf("pText(0.0005) = %q", got)
	}
}

func TestJudgeExpectation_ShowsAPOnlyForATestedSpeedAndReadsEqualAsTheOtherWay(t *testing.T) {
	show := func(v float64) string { return fmt.Sprint(v) }
	down := expect.Expectation{Metric: "edit-suite", Stat: "p50", Direction: "down"}
	up := expect.Expectation{Metric: "edit-suite", Stat: "p50", Direction: "up"}
	row := func(before, after, p float64, rate bool) ExpectationRow {
		return ExpectationRow{Before: before, AfterValue: after, P: p, Rate: rate, Clear: true, BeforeN: 5, AfterN: 5}
	}
	if d := judgeExpectation(row(2, 1, 0.01, false), down, show).Description; d != "held: 2 -> 1 (n 5 -> 5, p 0.010)" {
		t.Errorf("down, fell: %q", d)
	}
	if d := judgeExpectation(row(2, 1, 0, false), down, show).Description; strings.Contains(d, "p ") {
		t.Errorf("an untested p of 0 was shown: %q", d)
	}
	if d := judgeExpectation(row(2, 1, 0.01, true), down, show).Description; strings.Contains(d, ", p ") {
		t.Errorf("a rate showed a p: %q", d)
	}
	if got := judgeExpectation(row(2, 3, 0.01, false), up, show).State; got != expectHeld {
		t.Errorf("up, rose: %q", got)
	}
	for _, e := range []expect.Expectation{down, up} {
		if got := judgeExpectation(row(2, 2, 0.01, false), e, show).State; got != expectNotHeld {
			t.Errorf("%s, clear but equal: %q, want not held", e.Direction, got)
		}
	}
	if got := judgeExpectation(row(2, 1, 0.01, false), up, show).State; got != expectNotHeld {
		t.Errorf("up, fell: %q", got)
	}
}
