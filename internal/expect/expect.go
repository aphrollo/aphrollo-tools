// Package expect is the PR body's `expect:` line: the metric a change should
// move, checked later against the report. The metric names are data, a fixed
// table of what the report already computes.
package expect

import (
	"fmt"
	"slices"
	"strings"
)

// Metric is one thing the report measures and a PR may expect to move.
type Metric struct {
	// Name is what a PR body says.
	Name string
	// Source is where the report reads it: a speed row class, or the wrong-block rate.
	Source string
	// Stats are the readings a PR may name.
	Stats []string
	// Better is the direction that counts as an improvement.
	Better string
}

// SourceWrongBlocks is the Source of the wrong-block rate: waived denies over denies.
const SourceWrongBlocks = "wrong blocks"

var speedStats = []string{"p50", "p90"}

// Metrics is the table. A new metric is a row here and, for a speed class, the
// class of the same Source in the report.
var Metrics = []Metric{
	{"edit-suite", "edit suite", speedStats, "down"},
	{"edit-to-verdict", "edit to verdict", speedStats, "down"},
	{"commit-gate", "commit gate", speedStats, "down"},
	{"commit-gate-total", "commit gate total", speedStats, "down"},
	{"merge-gate", "merge gate", speedStats, "down"},
	{"merge-gate-total", "merge gate total", speedStats, "down"},
	{"mutation-commit", "mutation (commit)", speedStats, "down"},
	{"merge-queue", "merge queue", speedStats, "down"},
	{"ci-pipeline", "CI pipeline", speedStats, "down"},
	{"pr-lead-time", "PR lead time", speedStats, "down"},
	{"wrong-blocks", SourceWrongBlocks, []string{"rate"}, "down"},
}

// Expectation is one `expect:` line.
type Expectation struct {
	Metric, Stat, Direction string
}

func (e Expectation) String() string { return e.Metric + " " + e.Stat + " " + e.Direction }

// Find is the table row named name.
func Find(name string) (Metric, bool) {
	i := slices.IndexFunc(Metrics, func(m Metric) bool { return m.Name == name })
	if i < 0 {
		return Metric{}, false
	}
	return Metrics[i], true
}

const prefix = "expect:"

// Parse reads every `expect:` line of a PR body. A body with none is no
// expectation; a line it cannot read is an error that names the valid metrics.
func Parse(body string) ([]Expectation, error) {
	var out []Expectation
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
			continue
		}
		e, err := parseLine(strings.Fields(line[len(prefix):]))
		if err != nil {
			return nil, fmt.Errorf("%q: %s; write `expect: <metric> <p50|p90|rate> <down|up>` with a metric from: %s", line, err, names())
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

func parseLine(f []string) (Expectation, error) {
	if len(f) != 3 {
		return Expectation{}, fmt.Errorf("it has %d words after expect:, want 3", len(f))
	}
	e := Expectation{f[0], f[1], f[2]}
	m, ok := Find(e.Metric)
	switch {
	case !ok:
		return e, fmt.Errorf("%q is not a metric", e.Metric)
	case !slices.Contains(m.Stats, e.Stat):
		return e, fmt.Errorf("%s has no %q reading, only %s", m.Name, e.Stat, strings.Join(m.Stats, "|"))
	case e.Direction != "down" && e.Direction != "up":
		return e, fmt.Errorf("the direction is %q, not down|up", e.Direction)
	}
	return e, nil
}

func names() string {
	parts := make([]string, len(Metrics))
	for i, m := range Metrics {
		parts[i] = fmt.Sprintf("%s (%s; %s is better)", m.Name, strings.Join(m.Stats, "|"), m.Better)
	}
	return strings.Join(parts, ", ")
}

// Encode is the expectations as one merge-event detail value.
func Encode(es []Expectation) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.String()
	}
	return strings.Join(parts, ";")
}

// Decode reads what Encode wrote, dropping a record it cannot read.
func Decode(s string) []Expectation {
	var out []Expectation
	for _, part := range strings.Split(s, ";") {
		if e, err := parseLine(strings.Fields(part)); err == nil {
			out = append(out, e)
		}
	}
	return out
}
