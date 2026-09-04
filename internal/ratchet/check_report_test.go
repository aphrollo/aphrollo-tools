package ratchet

import "testing"

// A Finding with no line number (a file-scoped hit, not a specific line) must
// read as the bare file name — the ":0" a naive `> `-vs-`>=` slip would add
// is worse than nothing, since 0 is never a real line.
func TestResultLines_omitsLineNumberWhenFindingLineIsZero(t *testing.T) {
	r := Result{Findings: []Finding{{
		Law: "law", File: "a.go", Line: 0, What: "bad", Baseline: 1, Measured: 2,
	}}}
	lines := r.Lines()
	if len(lines) != 1 {
		t.Fatalf("Lines() = %v, want one line", lines)
	}
	const want = "law: a.go bad (baseline 1, now 2)"
	if lines[0] != want {
		t.Fatalf("Lines()[0] = %q, want %q", lines[0], want)
	}
}

// fitWhat must return the text UNCHANGED when it exactly fills the budget —
// a `<=` slipping to `<` would truncate one rune early on every hit that
// lands exactly on the line-width ceiling.
func TestFitWhat_keepsTextUnchangedWhenRuneCountEqualsBudget(t *testing.T) {
	got := fitWhat("hello", 5)
	if got != "hello" {
		t.Fatalf("fitWhat(%q, 5) = %q, want unchanged %q", "hello", got, "hello")
	}
}

// A budget of three runes is too small to say anything — even one truncated
// character plus the ellipsis would be misleading — so it yields nothing.
func TestFitWhat_returnsEmptyWhenBudgetIsThreeRunes(t *testing.T) {
	got := fitWhat("hello", 3)
	if got != "" {
		t.Fatalf("fitWhat(%q, 3) = %q, want empty", "hello", got)
	}
}

// Four runes is exactly enough for three characters plus the ellipsis — the
// smallest budget that still says something. This pins the other side of the
// same boundary the empty-at-three case pins.
func TestFitWhat_truncatesToThreeCharsPlusEllipsisWhenBudgetIsFourRunes(t *testing.T) {
	got := fitWhat("hello", 4)
	const want = "hel…"
	if got != want {
		t.Fatalf("fitWhat(%q, 4) = %q, want %q", "hello", got, want)
	}
}
