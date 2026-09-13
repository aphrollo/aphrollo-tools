package ratchet

import (
	"strings"
	"testing"
)

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

// A dep-graph-ceiling hit has no line — it is a whole-root measurement filed
// at line 0 — so escapeWindow's "on the line or the line above" names a
// position that does not exist and the kind does not accept. Advice that names
// a window the law refuses is worse than none: the escape is read from the
// ROOT's own Cargo.toml, in any `#` comment line of it.
func TestRemedyFor_DepGraphCeilingNamesTheRootManifest(t *testing.T) {
	law, err := ParseLaw(crateFanoutLaw, "crate-fanout")
	if err != nil {
		t.Fatal(err)
	}

	got := remedyFor(law)

	for _, want := range []string{"crate-fanout-ok:", "Cargo.toml"} {
		if !strings.Contains(got, want) {
			t.Errorf("remedyFor = %q, want it to name %q", got, want)
		}
	}
	if strings.Contains(got, "the line above") {
		t.Errorf("remedyFor = %q, but a hit filed at line 0 has no line above it", got)
	}
}

// The escape is what the remedy has to describe: a ceiling law that declares
// none still has exactly one way through, and must say so rather than point at
// a comment nobody may write.
func TestRemedyFor_DepGraphCeilingWithNoEscapeSaysThereIsNone(t *testing.T) {
	law, err := ParseLaw(strings.ReplaceAll(crateFanoutLaw, "escape = \"crate-fanout-ok:\"\n", ""), "crate-fanout")
	if err != nil {
		t.Fatal(err)
	}

	if got := remedyFor(law); got != "no escape: lower the code" {
		t.Fatalf("remedyFor = %q, want the no-escape remedy", got)
	}
}

// A marker-within-lines refusal that only ever describes the marker as
// something to add ABOVE the trigger sends the reader to grow the file by a
// line — which, in a file already at its module_size ceiling, trades the
// fixed hit here for a new one there (issue #652: a first pass following that
// reading fixed 16 hits and raised 4 module_size baselines). The marker on
// the trigger's OWN line costs no lines and both laws accept it, so the
// remedy must name that option and say why it is the cheap one.
func TestRemedyFor_MarkerWithinLinesNamesTheOwnLineOption(t *testing.T) {
	law, err := ParseLaw(`name = "bound-marker"
description = "a growing collection field states its bound"
severity = "deny"

[scope]
include = ["**/*.rs"]

[matcher]
kind = "marker-within-lines"
trigger = "channels: Vec<"
marker = "// bound:"
lines = 2
`, "bound-marker")
	if err != nil {
		t.Fatal(err)
	}

	got := remedyFor(law)

	if !strings.Contains(got, "own line") {
		t.Fatalf("remedyFor = %q, want it to name the trigger's own line", got)
	}
	if !strings.Contains(got, "module_size") {
		t.Fatalf("remedyFor = %q, want it to say why the own-line option matters", got)
	}
}
