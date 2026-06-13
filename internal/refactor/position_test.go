package refactor

import "testing"

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
