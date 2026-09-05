package refactor

import (
	"strings"
	"testing"
)

func TestSymbolColumn(t *testing.T) {
	cases := []struct {
		line, symbol string
		want         int
	}{
		{"foo bar baz", "bar", 4},
		{"café x = 1", "x", 5}, // é is 2 bytes but 1 UTF-16 unit
		{"  indented", "indented", 2},
	}
	for _, c := range cases {
		got, err := symbolColumn(c.line, c.symbol)
		if err != nil {
			t.Fatalf("symbolColumn(%q,%q): %v", c.line, c.symbol, err)
		}
		if got != c.want {
			t.Fatalf("symbolColumn(%q,%q) = %d, want %d", c.line, c.symbol, got, c.want)
		}
	}

	if _, err := symbolColumn("foo bar", "missing"); err == nil {
		t.Fatalf("symbolColumn for absent symbol: want error, got nil")
	}
}

// A symbol appearing more than once on the line is ambiguous: anchoring
// silently on the first occurrence (the old behavior) could rename the wrong
// one of `x := x + x`'s three x's. symbolColumn must refuse and name every
// candidate column so the caller can pick one via --col.
func TestSymbolColumn_RefusesAmbiguousMultipleOccurrences(t *testing.T) {
	_, err := symbolColumn("x := x + x", "x")
	if err == nil {
		t.Fatalf("symbolColumn(%q,%q): want error for 3 occurrences, got nil", "x := x + x", "x")
	}
	for _, want := range []string{"1", "6", "10"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("symbolColumn ambiguity error %q missing column %s", err.Error(), want)
		}
	}
}

// A different repeated-occurrence shape (a function call repeating its own
// name as an argument) must be refused the same way.
func TestSymbolColumn_RefusesAmbiguousCallArgument(t *testing.T) {
	if _, err := symbolColumn("foo(foo, foo)", "foo"); err == nil {
		t.Fatalf("symbolColumn(%q,%q): want error for 3 occurrences, got nil", "foo(foo, foo)", "foo")
	}
}
