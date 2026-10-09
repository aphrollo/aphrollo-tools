package measure

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// abEscapeLanes is n lanes of arm, the first bad of which carry one escape record each.
func abEscapeLanes(arm string, n, bad int) []tdd.Event {
	var out []tdd.Event
	for i := range n {
		lane := fmt.Sprintf("%s-%d", arm, i)
		out = append(out, ev(float64(i), lane, "lane-arm", detail("arm", arm, "why", "assigned", "mode", arm)))
		if i < bad {
			out = append(out, ev(float64(i)+0.5, lane, "escape", func(e *tdd.Event) { e.Verdict = "escaped" }))
		}
	}
	return out
}

func metricRow(t *testing.T, ab AB, name string) ABMetric {
	t.Helper()
	for _, m := range ab.Metrics {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no metric %q in %+v", name, ab.Metrics)
	return ABMetric{}
}

func TestBootstrapDiff_AConstantSampleGivesAPointInterval(t *testing.T) {
	zeros, ones := []float64{0, 0, 0, 0}, []float64{1, 1, 1, 1}
	d, lo, hi := bootstrapDiff(zeros, ones, meanOf)
	if d != -1 || lo != -1 || hi != -1 {
		t.Errorf("mean diff = %v [%v, %v], want -1 [-1, -1]", d, lo, hi)
	}
	d, lo, hi = bootstrapDiff([]float64{10, 10, 10}, []float64{100, 100, 100}, medianOf)
	if d != -90 || lo != -90 || hi != -90 {
		t.Errorf("median diff = %v [%v, %v], want -90 [-90, -90]", d, lo, hi)
	}
}

func TestBootstrapDiff_TwoEqualMixedSamplesStraddleZero(t *testing.T) {
	s := []float64{0, 1, 0, 1, 0, 1, 0, 1}
	d, lo, hi := bootstrapDiff(s, s, meanOf)
	if d != 0 || lo >= 0 || hi <= 0 || lo < -1 || hi > 1 {
		t.Errorf("diff = %v [%v, %v], want 0 with an interval across 0 inside [-1, 1]", d, lo, hi)
	}
}

func TestComputeAB_TheSameLogGivesTheSameBytes(t *testing.T) {
	events := abCat(abEscapeLanes("enforce", 9, 5), abEscapeLanes("warn", 9, 2))
	first, second := computeAB(events, Options{}).Text(), computeAB(events, Options{}).Text()
	if first != second {
		t.Errorf("two reads of one log differ:\n%s\n---\n%s", first, second)
	}
}

func TestComputeAB_EnforceWithMoreEscapesIsDecidedWarnBetter(t *testing.T) {
	ab := computeAB(abCat(abEscapeLanes("enforce", 12, 12), abEscapeLanes("warn", 12, 0)), Options{})
	m := metricRow(t, ab, "escapes per lane")
	if m.Verdict != VerdictWarnBetter || !ab.Decidable || ab.Verdict != VerdictWarnBetter {
		t.Errorf("verdict = %q (readout %q, decidable %v), want %q", m.Verdict, ab.Verdict, ab.Decidable, VerdictWarnBetter)
	}
}

func TestComputeAB_EnforceWithFewerEscapesIsDecidedEnforceBetter(t *testing.T) {
	ab := computeAB(abCat(abEscapeLanes("enforce", 12, 0), abEscapeLanes("warn", 12, 12)), Options{})
	if m := metricRow(t, ab, "escapes per lane"); m.Verdict != VerdictEnforceBetter || !ab.Decidable {
		t.Errorf("verdict = %q decidable %v, want %q", m.Verdict, ab.Decidable, VerdictEnforceBetter)
	}
}

func TestComputeAB_AnIntervalInsideTheBandIsNoMeaningfulDifference(t *testing.T) {
	ab := computeAB(abCat(abEscapeLanes("enforce", 12, 0), abEscapeLanes("warn", 12, 0)), Options{})
	if m := metricRow(t, ab, "escapes per lane"); m.Verdict != VerdictNoDifference || !ab.Decidable {
		t.Errorf("verdict = %q decidable %v, want %q", m.Verdict, ab.Decidable, VerdictNoDifference)
	}
}

func TestComputeAB_AWideIntervalOnAFewLanesIsStillDeciding(t *testing.T) {
	ab := computeAB(abCat(abEscapeLanes("enforce", 3, 1), abEscapeLanes("warn", 3, 0)), Options{})
	if m := metricRow(t, ab, "escapes per lane"); m.Verdict != VerdictDeciding || ab.Decidable {
		t.Errorf("verdict = %q decidable %v, want %q and not decidable", m.Verdict, ab.Decidable, VerdictDeciding)
	}
}

func TestComputeAB_OneLaneAnArmHasNoIntervalAndIsDeciding(t *testing.T) {
	ab := computeAB(abCat(abEscapeLanes("enforce", 1, 1), abEscapeLanes("warn", 1, 0)), Options{})
	m := metricRow(t, ab, "escapes per lane")
	if m.Verdict != VerdictDeciding || m.HasInterval {
		t.Errorf("verdict = %q, interval %v, want deciding with no interval at n 1", m.Verdict, m.HasInterval)
	}
}

func TestComputeAB_FiftyLanesAnArmWithNoDecisionIsMaxReached(t *testing.T) {
	ab := computeAB(abCat(abEscapeLanes("enforce", MaxABLanes, 25), abEscapeLanes("warn", MaxABLanes, 25)), Options{})
	m := metricRow(t, ab, "escapes per lane")
	if m.Verdict != VerdictMaxReached || !ab.Decidable {
		t.Fatalf("verdict = %q decidable %v, want %q", m.Verdict, ab.Decidable, VerdictMaxReached)
	}
	if got := ab.Text(); !strings.Contains(got, "too small to measure — decide on friction and cost") {
		t.Errorf("text lacks the max-reached sentence:\n%s", got)
	}
}

func TestAB_TextPrintsEachMetricWithItsIntervalAndNPerArm(t *testing.T) {
	got := computeAB(abCat(abEscapeLanes("enforce", 12, 12), abEscapeLanes("warn", 12, 0)), Options{}).Text()
	want := "escapes per lane (primary): enforce 1.00 (n 12), warn 0.00 (n 12); enforce minus warn +1.00, 90% interval [+1.00, +1.00]; decided: warn better"
	if !strings.Contains(got, want) {
		t.Errorf("text lacks %q:\n%s", want, got)
	}
	for _, m := range []string{"denies per lane (guardrail)", "time to green p50 (guardrail)"} {
		if !strings.Contains(got, m) {
			t.Errorf("text lacks the metric %q:\n%s", m, got)
		}
	}
}

// Language rows are always printed, one per arm for every language either arm met.
func TestAB_TextAlwaysPrintsLanguageRows(t *testing.T) {
	none := computeAB(abEscapeLanes("enforce", 2, 0), Options{}).Text()
	if !strings.Contains(none, "languages: none recorded") {
		t.Errorf("an empty language table says nothing:\n%s", none)
	}
	events := abCat(
		abLane(0, "lane/e1", "enforce", "go", "block"),
		abLane(0, "lane/w1", "warn", "ts", "warn"),
	)
	got := computeAB(events, Options{}).Text()
	for _, row := range []string{"enforce  go ", "warn     go ", "enforce  ts ", "warn     ts "} {
		if !strings.Contains(got, row) {
			t.Errorf("text lacks the language row %q (zero-filled for the arm that met none):\n%s", row, got)
		}
	}
}

// One escape in forty lanes moves the mean by 0.025, but the interval around it reaches
// past the 0.05 band: that is not "no meaningful difference", it is not yet known.
func TestComputeAB_AnIntervalWiderThanTheBandIsNotNoMeaningfulDifference(t *testing.T) {
	ab := computeAB(abCat(abEscapeLanes("enforce", 40, 1), abEscapeLanes("warn", 40, 0)), Options{})
	if m := metricRow(t, ab, "escapes per lane"); m.Verdict != VerdictDeciding {
		t.Errorf("verdict = %q [%v, %v], want %q", m.Verdict, m.Lo, m.Hi, VerdictDeciding)
	}
}

// The 90% interval of 2000 draws runs from the 100th to the 1900th value, 1-based.
func TestIntervalRanks_AreTheFifthAndNinetyFifthNearestRank(t *testing.T) {
	if lo, hi := intervalRanks(2000); lo != 99 || hi != 1899 {
		t.Errorf("ranks = %d, %d, want 99 and 1899", lo, hi)
	}
	if lo, hi := intervalRanks(20); lo != 0 || hi != 18 {
		t.Errorf("ranks of 20 = %d, %d, want 0 and 18", lo, hi)
	}
}

func TestVersionAtLeast_ComparesNumericallyAndTakesEqualAsEnough(t *testing.T) {
	for _, c := range []struct {
		v, min string
		want   bool
	}{
		{"1.30.1", "1.30.1", true}, {"1.30.2", "1.30.1", true}, {"1.30.0", "1.30.1", false},
		{"1.9.9", "1.30.1", false}, {"2.0.0", "1.30.1", true}, {"1.31.0", "1.30.9", true},
		{"v1.31.0-rc1", "1.30.1", true}, {"unknown", "1.30.1", false}, {"", "1.30.1", false},
	} {
		if got := versionAtLeast(c.v, c.min); got != c.want {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", c.v, c.min, got, c.want)
		}
	}
}
