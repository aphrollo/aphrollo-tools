package measure

import (
	"fmt"
	"math/rand/v2"
	"sort"
)

// MaxABLanes is the lanes an arm may hold before the A/B stops waiting for a decision:
// with no metric decided by then, the experiment is too small to measure.
const MaxABLanes = 50

// The verdict words of a metric, and of the readout (the primary metric's).
const (
	VerdictDeciding      = "deciding"
	VerdictEnforceBetter = "decided: enforce better"
	VerdictWarnBetter    = "decided: warn better"
	VerdictNoDifference  = "no meaningful difference"
	VerdictMaxReached    = "max reached"
)

// TooSmall is what the readout adds when the maximum is reached with nothing decided.
const TooSmall = "too small to measure — decide on friction and cost"

// abBootstrapDraws and the seed fix the bootstrap: one log, one set of bytes.
const abBootstrapDraws = 2000

func abRand() *rand.Rand { return rand.New(rand.NewPCG(20261009, 0x0a0b)) }

// abMetricDef is one pre-registered metric, as data: its name, role, the band inside which
// a difference is no difference, how a sample of per-lane values is summarised, and how
// a lane gives its value. A lower value is better for every metric.
type abMetricDef struct {
	Name   string
	Role   string
	Band   float64
	Format func(float64) string
	Stat   func([]float64) float64
	Sample func(*abLaneFacts) (float64, bool)
}

// abMetrics is the table the A/B is judged by, fixed before the data: the primary
// metric first. Adding a metric is a row, not control flow.
var abMetrics = []abMetricDef{
	{"escapes per lane", "primary", 0.05, perLane, meanOf,
		func(f *abLaneFacts) (float64, bool) { return float64(f.records + f.ciRed), true }},
	{"time to green p50", "guardrail", 60, secs, medianOf,
		func(f *abLaneFacts) (float64, bool) {
			if f.greenAfter.IsZero() {
				return 0, false
			}
			return f.greenAfter.Sub(f.firstAt).Seconds(), true
		}},
	{"denies per lane", "guardrail", 0.5, perLane, meanOf,
		func(f *abLaneFacts) (float64, bool) { return float64(f.denies), true }},
}

func perLane(v float64) string { return fmt.Sprintf("%.2f", v) }

// ABMetric is one metric's readout: each arm's value and n, the difference enforce minus
// warn with its 90% interval, and the verdict.
type ABMetric struct {
	Name        string  `json:"name"`
	Role        string  `json:"role"`
	Band        float64 `json:"band"`
	Enforce     float64 `json:"enforce"`
	Warn        float64 `json:"warn"`
	NEnforce    int     `json:"n_enforce"`
	NWarn       int     `json:"n_warn"`
	Diff        float64 `json:"diff"`
	Lo          float64 `json:"lo"`
	Hi          float64 `json:"hi"`
	HasInterval bool    `json:"has_interval"`
	Verdict     string  `json:"verdict"`
}

func meanOf(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

// medianOf is the nearest-rank median, as the p50 of a Dist is.
func medianOf(s []float64) float64 {
	c := append([]float64(nil), s...)
	sort.Float64s(c)
	return percentile(c, 0.5)
}

// bootstrapDiff is stat(a) minus stat(b) and the 90% percentile-bootstrap interval of it,
// drawn from a fixed seed. Both samples need at least one value.
func bootstrapDiff(a, b []float64, stat func([]float64) float64) (diff, lo, hi float64) {
	diff = stat(a) - stat(b)
	r := abRand()
	ra, rb := make([]float64, len(a)), make([]float64, len(b))
	diffs := make([]float64, abBootstrapDraws)
	for i := range diffs {
		for j := range ra {
			ra[j] = a[r.IntN(len(a))]
		}
		for j := range rb {
			rb[j] = b[r.IntN(len(b))]
		}
		diffs[i] = stat(ra) - stat(rb)
	}
	sort.Float64s(diffs)
	return diff, diffs[abBootstrapDraws/20-1], diffs[abBootstrapDraws*19/20-1]
}

// judge is the stop rule: decided when the interval excludes 0 or lies inside the band;
// at the maximum lanes an arm with neither is "max reached"; otherwise still deciding.
func judge(m ABMetric, lanesE, lanesW int) string {
	if m.HasInterval {
		switch {
		case m.Lo > 0:
			return VerdictWarnBetter
		case m.Hi < 0:
			return VerdictEnforceBetter
		case m.Lo > -m.Band && m.Hi < m.Band:
			return VerdictNoDifference
		}
	}
	if lanesE >= MaxABLanes && lanesW >= MaxABLanes {
		return VerdictMaxReached
	}
	return VerdictDeciding
}

// foldMetrics reads every metric of the table off the per-arm lane samples.
func foldMetrics(lanes map[string][]*abLaneFacts) ([]ABMetric, string, bool) {
	var out []ABMetric
	for _, def := range abMetrics {
		var s [2][]float64
		for i, arm := range abArms {
			for _, f := range lanes[arm] {
				if v, ok := def.Sample(f); ok {
					s[i] = append(s[i], v)
				}
			}
			sort.Float64s(s[i])
		}
		m := ABMetric{Name: def.Name, Role: def.Role, Band: def.Band, NEnforce: len(s[0]), NWarn: len(s[1])}
		if len(s[0]) > 0 {
			m.Enforce = def.Stat(s[0])
		}
		if len(s[1]) > 0 {
			m.Warn = def.Stat(s[1])
		}
		if len(s[0]) >= 2 && len(s[1]) >= 2 {
			m.Diff, m.Lo, m.Hi = bootstrapDiff(s[0], s[1], def.Stat)
			m.HasInterval = true
		} else {
			m.Diff = m.Enforce - m.Warn
		}
		m.Verdict = judge(m, len(lanes[abArms[0]]), len(lanes[abArms[1]]))
		out = append(out, m)
	}
	primary := out[0].Verdict
	return out, primary, primary != VerdictDeciding
}

func signed(f func(float64) string, v float64) string {
	if v < 0 {
		return "-" + f(-v)
	}
	return "+" + f(v)
}

// text is the metric as the line `stats --ab` prints.
func (m ABMetric) text() string {
	format := perLane
	for _, def := range abMetrics {
		if def.Name == m.Name {
			format = def.Format
		}
	}
	head := fmt.Sprintf("%s (%s): enforce %s (n %d), warn %s (n %d)", m.Name, m.Role,
		format(m.Enforce), m.NEnforce, format(m.Warn), m.NWarn)
	if !m.HasInterval {
		return head + "; no interval under 2 per arm; " + m.Verdict
	}
	return fmt.Sprintf("%s; enforce minus warn %s, 90%% interval [%s, %s]; %s", head,
		signed(format, m.Diff), signed(format, m.Lo), signed(format, m.Hi), m.Verdict)
}
