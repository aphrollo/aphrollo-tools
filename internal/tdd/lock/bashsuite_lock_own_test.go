package lock

import "testing"

// TestIsSettledVerdict_MatchesPrefixesAndSuffixesNotJustExactWords covers
// every family the budget floor and recordedSuiteSecs read this through: a
// bare "green"/"red", a hyphenated variant of either, "no-delta", and a
// "-blocked" verdict all mean the suite ran to completion. Anything else —
// including a verdict that only shares a substring with one of these, like
// "greenish" — must read as NOT settled, since an unrecognised verdict is
// "no fresh answer", never a guess that it finished.
func TestIsSettledVerdict_MatchesPrefixesAndSuffixesNotJustExactWords(t *testing.T) {
	cases := []struct {
		verdict string
		want    bool
	}{
		{"green", true},
		{"green-cache-hit", true},
		{"red", true},
		{"red-missing-impl", true},
		{"no-delta", true},
		{"law-blocked", true},
		{"timeout-rejected", false},
		{"cache-hit", false},
		{"", false},
		{"greenish", false},
	}
	for _, c := range cases {
		if got := isSettledVerdict(c.verdict); got != c.want {
			t.Errorf("isSettledVerdict(%q) = %v, want %v", c.verdict, got, c.want)
		}
	}
}
