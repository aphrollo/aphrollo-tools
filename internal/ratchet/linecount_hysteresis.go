package ratchet

// Hysteresis on a line-count ceiling.
//
// A single threshold oscillates. A file recorded at 607 lines against a
// ceiling of 600 clears its baseline row the moment it measures 599 — and the
// cheapest way to 599 is to delete eight lines of comment, not to split the
// file. The next edit spends the room again, the one after that shaves
// another comment, and the file sits at the ceiling forever, paying its line
// budget with the explanation the next reader needed. Advice ("split the
// file") loses to a gate that passes.
//
// So a file that has crossed the ceiling is held to a LOWER bar on the way
// back: its row is cleared only once it measures at or below the re-entry
// bar, by default 90% of the ceiling. No shave reaches that; a split does.
// The bar governs the EXIT from a baseline and nothing else — a file with no
// row is judged against the ceiling exactly as it always was, and nothing
// here ever creates or raises a row.

// reentryPctOfMax is the default re-entry bar as a percentage of the law's
// own ceiling, used by any line-count law that declares no `reentry`. 90% is
// far enough below the ceiling that no comment shave reaches it (60 lines on
// a 600-line ceiling) and close enough that a genuine split clears it in one
// move.
const reentryPctOfMax = 90

// defaultReentry is the bar a line-count law gets when it declares none.
func defaultReentry(max int) int {
	return max * reentryPctOfMax / 100
}

// lineCountCeiling is the count key may reach before it produces a hit: the
// law's ceiling for a key the baseline has never seen, the re-entry bar for
// one it already carries.
func (l Law) lineCountCeiling(key string) int {
	if l.Baselined[key] {
		return l.Matcher.Reentry
	}
	return l.Matcher.Max
}

// baselinedKeys is the set of keys a law's baseline already carries — what
// lineCountCeiling reads to tell a file coming back DOWN to its bar from one
// crossing the ceiling for the first time. Loaded before the scan, because
// the matcher needs it while it measures.
func baselinedKeys(baseline *Baseline) map[string]bool {
	counts := baseline.Counts()
	if len(counts) == 0 {
		return nil
	}
	keys := make(map[string]bool, len(counts))
	for k := range counts {
		keys[k] = true
	}
	return keys
}
