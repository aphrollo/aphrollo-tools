package ratchet

import (
	"path/filepath"
	"testing"
)

func goBenchCeilingLaw(t *testing.T) Law {
	t.Helper()
	law, err := ParseLaw(`
name = "bench"
description = "a go benchmark's B/op and allocs/op may only fall"
severity = "deny"
baseline = ".ratchet/baselines/bench.txt"

[scope]
include = ["**/*.txt"]

[matcher]
kind = "go-bench-ceiling"
files = "bench/baseline.txt"
`, "bench")
	if err != nil {
		t.Fatal(err)
	}
	return law
}

// TestGoBenchCeiling_ReadsBPerOpAndAllocsPerOpByName proves the matcher reads
// exactly the two enforced columns — never ns/op, which is wall-clock noise
// this law does not judge — and keys each by benchmark name and metric.
func TestGoBenchCeiling_ReadsBPerOpAndAllocsPerOpByName(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "bench", "baseline.txt"),
		"BenchmarkFoo-8   \t5\t123 ns/op\t456 B/op\t7 allocs/op\n")

	hits, err := goBenchCeilingHits(diskView(root), goBenchCeilingLaw(t), true)
	if err != nil {
		t.Fatalf("goBenchCeilingHits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %v, want one for B/op and one for allocs/op", keys(hits))
	}
	byMetric := map[string]int{}
	for _, h := range hits {
		byMetric[h.Key] = h.Weight
	}
	if byMetric["bench/baseline.txt|BenchmarkFoo|B/op"] != 456 {
		t.Errorf("B/op = %+v, want 456", hits)
	}
	if byMetric["bench/baseline.txt|BenchmarkFoo|allocs/op"] != 7 {
		t.Errorf("allocs/op = %+v, want 7", hits)
	}
}

// TestGoBenchCeiling_TakesTheWorstOfRepeatedLines proves a `-count`
// re-record (several lines for the same benchmark name) is judged by its
// WORST line, not its best — picking the best of several runs would hide a
// regression the other runs actually show.
func TestGoBenchCeiling_TakesTheWorstOfRepeatedLines(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "bench", "baseline.txt"),
		"BenchmarkFoo-8   \t5\t100 ns/op\t400 B/op\t5 allocs/op\n"+
			"BenchmarkFoo-8   \t5\t100 ns/op\t900 B/op\t5 allocs/op\n"+
			"BenchmarkFoo-8   \t5\t100 ns/op\t500 B/op\t9 allocs/op\n")

	hits, err := goBenchCeilingHits(diskView(root), goBenchCeilingLaw(t), true)
	if err != nil {
		t.Fatalf("goBenchCeilingHits: %v", err)
	}
	byMetric := map[string]int{}
	for _, h := range hits {
		byMetric[h.Key] = h.Weight
	}
	if byMetric["bench/baseline.txt|BenchmarkFoo|B/op"] != 900 {
		t.Errorf("B/op = %+v, want the worst of the three lines, 900", hits)
	}
	if byMetric["bench/baseline.txt|BenchmarkFoo|allocs/op"] != 9 {
		t.Errorf("allocs/op = %+v, want the worst of the three lines, 9", hits)
	}
}

// TestGoBenchCeiling_IgnoresNonBenchLines proves `PASS`/`ok`/package header
// lines a go test -bench run also prints never turn into hits.
func TestGoBenchCeiling_IgnoresNonBenchLines(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "bench", "baseline.txt"),
		"goos: linux\ngoarch: amd64\npkg: example.com/x\n"+
			"BenchmarkFoo-8   \t5\t100 ns/op\t400 B/op\t5 allocs/op\n"+
			"PASS\nok  \texample.com/x\t1.234s\n")

	hits, err := goBenchCeilingHits(diskView(root), goBenchCeilingLaw(t), true)
	if err != nil {
		t.Fatalf("goBenchCeilingHits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %v, want exactly the two metrics for the one real benchmark line", keys(hits))
	}
}

// TestGoBenchCeiling_ErrorsWhenArmedOverNothing mirrors json-number-ceiling's
// own refusal: a law with nothing to read must fail loudly, never pass clean
// over data that does not exist.
func TestGoBenchCeiling_ErrorsWhenArmedOverNothing(t *testing.T) {
	if _, err := goBenchCeilingHits(diskView(t.TempDir()), goBenchCeilingLaw(t), true); err == nil {
		t.Fatal("armed with no data must be an error, never a pass")
	}
}

// TestGoBenchCeiling_OnlyFlagsARaisedBOrAllocsColumn is the end-to-end
// proof: Check refuses a re-record that raises B/op or allocs/op for a
// name, and tightens the baseline when a re-record only lowers it.
func TestGoBenchCeiling_OnlyFlagsARaisedBOrAllocsColumn(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "bench", `
name = "bench"
description = "a go benchmark's B/op and allocs/op may only fall"
severity = "deny"
baseline = ".ratchet/baselines/bench.txt"

[scope]
include = ["**/*.txt"]

[matcher]
kind = "go-bench-ceiling"
files = "bench/baseline.txt"
`)
	benchFile := filepath.Join(root, "bench", "baseline.txt")
	write(t, benchFile, "BenchmarkFoo-8   \t5\t100 ns/op\t400 B/op\t5 allocs/op\n")
	write(t, filepath.Join(root, ".ratchet", "baselines", "bench.txt"),
		"bench/baseline.txt|BenchmarkFoo|B/op | 400\nbench/baseline.txt|BenchmarkFoo|allocs/op | 5\n")

	// A re-record that RAISES B/op must be refused.
	write(t, benchFile, "BenchmarkFoo-8   \t5\t100 ns/op\t500 B/op\t5 allocs/op\n")
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %v, want exactly the one raised B/op row", res.Lines())
	}

	// A re-record that only LOWERS both columns must tighten, not flag.
	write(t, benchFile, "BenchmarkFoo-8   \t5\t100 ns/op\t300 B/op\t3 allocs/op\n")
	res, err = Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a lower re-record must not be a finding: %v", res.Lines())
	}
	got := read(t, filepath.Join(root, ".ratchet", "baselines", "bench.txt"))
	if got != "bench/baseline.txt|BenchmarkFoo|B/op | 300\nbench/baseline.txt|BenchmarkFoo|allocs/op | 3\n" {
		t.Errorf("baseline did not tighten to the lower re-record: %q", got)
	}
}
