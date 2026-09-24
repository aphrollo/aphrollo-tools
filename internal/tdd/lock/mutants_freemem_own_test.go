package lock

import (
	"strings"
	"testing"
)

// TestMutantsBudgetMemoryGB_PrefersTheSmallerMeasuredFree pins the safe
// direction: an available-memory reading below the RAM figure is what
// actually bounds the run (a box already busy with other sessions), and the
// budget must take the smaller of the two, never the larger.
func TestMutantsBudgetMemoryGB_PrefersTheSmallerMeasuredFree(t *testing.T) {
	gb, term := mutantsBudgetMemoryGB(63, 24)
	if gb != 24 || term != "free" {
		t.Fatalf("mutantsBudgetMemoryGB(63, 24) = (%d, %q), want the smaller measured 24GB labelled free", gb, term)
	}
}

// TestMutantsBudgetMemoryGB_IgnoresAFreeReadingLargerThanRAM is the other
// half: an idle box can report more available commit than it has RAM, and a
// budget that trusted that number would start more work than today's
// arithmetic — the term falls back to RAM instead.
func TestMutantsBudgetMemoryGB_IgnoresAFreeReadingLargerThanRAM(t *testing.T) {
	gb, term := mutantsBudgetMemoryGB(8, 64)
	if gb != 8 || term != "ram" {
		t.Fatalf("mutantsBudgetMemoryGB(8, 64) = (%d, %q), want RAM (8GB) — a free reading above RAM must never win", gb, term)
	}
}

// TestMutantsBudgetMemoryGB_UnreadableFreeFallsBackToRAM is the
// unknown-is-unknown rule: a zero (unreadable) free reading must not be
// treated as "nothing available"; the term falls back to RAM.
func TestMutantsBudgetMemoryGB_UnreadableFreeFallsBackToRAM(t *testing.T) {
	gb, term := mutantsBudgetMemoryGB(32, 0)
	if gb != 32 || term != "ram" {
		t.Fatalf("mutantsBudgetMemoryGB(32, 0) = (%d, %q), want RAM (32GB) when the free reading could not be taken", gb, term)
	}
}

// TestMutantsMemoryTerm_MeasuredFreeIsLabelled pins the report text a
// measured (not assumed) reading gets: the arithmetic, the divisor label and
// the "(measured)" suffix that tells a "free" decision from a "ram" one by
// reading the log alone.
func TestMutantsMemoryTerm_MeasuredFreeIsLabelled(t *testing.T) {
	byMem, term, text, known := mutantsMemoryTerm(63, 24, 8, "shard")
	if !known {
		t.Fatal("known = false, want true when a reading was taken")
	}
	if term != "free" || byMem != 3 {
		t.Fatalf("term=%q byMem=%d, want free/3 (24GB / 8 per shard)", term, byMem)
	}
	if !strings.Contains(text, "free 24GB/shard=3") || !strings.Contains(text, "(measured)") {
		t.Fatalf("text = %q, want the free arithmetic and the (measured) suffix", text)
	}
}

// TestMutantsMemoryTerm_RAMTermCarriesNoMeasuredSuffix is the other branch:
// a RAM-derived term must never claim to be measured, since it was assumed
// from the spec sheet rather than read off the running box.
func TestMutantsMemoryTerm_RAMTermCarriesNoMeasuredSuffix(t *testing.T) {
	_, term, text, known := mutantsMemoryTerm(32, 0, 4, "job")
	if !known {
		t.Fatal("known = false, want true when the RAM reading is available")
	}
	if term != "ram" {
		t.Fatalf("term = %q, want ram", term)
	}
	if strings.Contains(text, "measured") {
		t.Fatalf("text = %q, an assumed RAM term must not claim to be measured", text)
	}
}

// TestMutantsMemoryTerm_NeitherReadingKnownIsUnknown is the fallback when
// this box can answer neither question: known=false lets the caller fall
// back to deciding on cores alone rather than dividing by zero or guessing.
func TestMutantsMemoryTerm_NeitherReadingKnownIsUnknown(t *testing.T) {
	byMem, term, text, known := mutantsMemoryTerm(0, 0, 8, "shard")
	if known {
		t.Fatal("known = true, want false when neither RAM nor free could be read")
	}
	if byMem != 0 || term != "ram" || text != "ram unknown" {
		t.Fatalf("got (%d, %q, %q), want (0, \"ram\", \"ram unknown\")", byMem, term, text)
	}
}
