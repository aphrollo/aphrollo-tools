package ratchet

import (
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// baselineAlphabet is the small key set the multiset properties below draw
// from — small on purpose, so a run generates plenty of REPEATED keys, the
// case the multiset form exists for. A per-key identity a Baseline never saw
// before is not what "multiset" is testing.
var baselineAlphabet = []string{"a", "b", "c", "d"}

// keysGen draws a small multiset of keys, one occurrence per baseline line.
func keysGen(t *rapid.T, label string) []string {
	return rapid.SliceOfN(rapid.SampledFrom(baselineAlphabet), 0, 12).Draw(t, label)
}

// tally counts occurrences, the shape ParseBaseline's Multiset form and a
// scan's `measured` map both use.
func tally(keys []string) map[string]int {
	m := map[string]int{}
	for _, k := range keys {
		m[k]++
	}
	return m
}

// shuffledLines returns keys in a different, deterministic-per-call order —
// reversed — so two Baselines built from it carry the same multiset in a
// provably different line order without pulling in a second RNG.
func shuffledLines(keys []string) []string {
	out := append([]string(nil), keys...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func parseMultiset(t *testing.T, keys []string) *Baseline {
	t.Helper()
	b, err := ParseBaseline(strings.Join(keys, "\n")+"\n", Multiset)
	if err != nil {
		t.Fatalf("ParseBaseline(%v): %v", keys, err)
	}
	if len(keys) == 0 {
		// ParseBaseline("" ...) and ParseBaseline("\n", ...) both parse to an
		// empty baseline; the join above renders "" for an empty slice.
		b, err = ParseBaseline("", Multiset)
		if err != nil {
			t.Fatalf("ParseBaseline(empty): %v", err)
		}
	}
	return b
}

// parseCounted builds a Counted-form baseline with the same per-key ceilings
// as keys' tally, one `<key> | <count>` line per distinct key (Counted
// rejects a repeated key, so the multiset's repetition collapses into the
// count field instead of repeated lines).
func parseCounted(t *testing.T, keys []string) *Baseline {
	t.Helper()
	counts := tally(keys)
	names := make([]string, 0, len(counts))
	for k := range counts {
		names = append(names, k)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, k := range names {
		sb.WriteString(k)
		sb.WriteString(" | ")
		sb.WriteString(itoa(counts[k]))
		sb.WriteByte('\n')
	}
	b, err := ParseBaseline(sb.String(), Counted)
	if err != nil {
		t.Fatalf("ParseBaseline(%v, Counted): %v", keys, err)
	}
	return b
}

// changeSet renders a Tightening as a sort-stable, order-independent string
// so two runs that reached the same set of changes in a different ORDER
// compare equal.
func changeSet(t Tightening) string {
	var lines []string
	for _, c := range t.Lowered {
		lines = append(lines, "lower "+c.Key+" "+itoa(c.From)+"->"+itoa(c.To))
	}
	for _, c := range t.Removed {
		lines = append(lines, "remove "+c.Key+" "+itoa(c.From))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// TestBaselineMultiset_TightenNeverRaisesACeiling pins the MULTISET form's
// structural guarantee: Tighten only ever drops existing lines, so a key's
// physical line count can only fall or hold, never rise above what the file
// carried before the run — true by construction of the representation
// itself, not just of Tighten's arithmetic.
func TestBaselineMultiset_TightenNeverRaisesACeiling(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		baselineKeys := keysGen(rt, "baseline")
		measuredKeys := keysGen(rt, "measured")
		measured := tally(measuredKeys)

		b := parseMultiset(t, baselineKeys)
		before := b.Counts()
		b.Tighten(measured)
		after := b.Counts()

		for k, ceilingBefore := range before {
			if after[k] > ceilingBefore {
				rt.Fatalf("key %q: ceiling went %d -> %d — Tighten raised a baseline", k, ceilingBefore, after[k])
			}
		}
	})
}

// TestBaselineCounted_TightenNeverRaisesACeiling is the mutation-bearing
// closed-form half: the COUNTED form has no physical-line floor under it —
// TightenWithSites writes `l.count = keepUpTo` straight into the one line a
// key owns, so a computation that let `keepUpTo` exceed the OLD ceiling (a
// `min` that regressed to a `max`, say) would show up here as a real raise,
// not just a no-op. Same shared target-count arithmetic as the multiset
// form; this is the form that can actually catch it.
func TestBaselineCounted_TightenNeverRaisesACeiling(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		baselineKeys := keysGen(rt, "baseline")
		measuredKeys := keysGen(rt, "measured")
		measured := tally(measuredKeys)

		b := parseCounted(t, baselineKeys)
		before := b.Counts()
		b.Tighten(measured)
		after := b.Counts()

		for k, ceilingBefore := range before {
			if after[k] > ceilingBefore {
				rt.Fatalf("key %q: ceiling went %d -> %d — Tighten raised a baseline", k, ceilingBefore, after[k])
			}
		}
	})
}

// TestBaselineMultiset_HitOrderNeverChangesTheVerdict: a baseline file is
// hand-merged, so two branches can reorder the SAME multiset of lines with
// no semantic change — a merge conflict resolved by picking one branch's
// ordering must never look like a different ceiling to Tighten or
// Regressions than the other branch's ordering would have.
func TestBaselineMultiset_HitOrderNeverChangesTheVerdict(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		baselineKeys := keysGen(rt, "baseline")
		measuredKeys := keysGen(rt, "measured")
		measured := tally(measuredKeys)

		inOrder := parseMultiset(t, baselineKeys)
		reordered := parseMultiset(t, shuffledLines(baselineKeys))

		// No pre-Tighten Counts() check here: Counts() sums `l.count` over
		// b.lines by plain map accumulation, which is commutative — no
		// mutation of the PARSER could make that sum depend on line order
		// without breaking every other test in this file first, so a check
		// at this point catches nothing real. The post-Tighten check below
		// is the one that can fail: Tighten decides WHICH duplicate physical
		// lines to drop, and that decision is where an order-dependent bug
		// would actually live.

		regIn := inOrder.Regressions(measured)
		regRe := reordered.Regressions(measured)
		if got, want := renderRegressions(regIn), renderRegressions(regRe); got != want {
			rt.Fatalf("same multiset, different line order: Regressions() = %q vs %q", got, want)
		}

		tightIn := inOrder.Tighten(measured)
		tightRe := reordered.Tighten(measured)
		if got, want := changeSet(tightIn), changeSet(tightRe); got != want {
			rt.Fatalf("same multiset, different line order: Tighten() = %q vs %q", got, want)
		}
		// And the two baselines converge to the same ceiling after tightening,
		// not just the same reported CHANGE — a bug that dropped the wrong
		// duplicate line could report identical changes while leaving
		// different survivors.
		if got, want := inOrder.Counts(), reordered.Counts(); !countsEqual(got, want) {
			rt.Fatalf("post-Tighten, different line order: Counts() = %v vs %v", got, want)
		}
	})
}

func countsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func renderRegressions(regs []Regression) string {
	var lines []string
	for _, r := range regs {
		lines = append(lines, r.Key+" "+itoa(r.Baseline)+" "+itoa(r.Measured))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
