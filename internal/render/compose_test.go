package render

import (
	"slices"
	"strings"
	"testing"
)

func TestTokens_roundsBytesUpToWholeTokens(t *testing.T) {
	for n, want := range map[int]int{0: 0, 1: 1, 4: 1, 5: 2, 240: 60, 241: 61, 480: 120} {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestCap_namesTheDocsCaps(t *testing.T) {
	want := map[Kind]int{KindDeny: 120, KindGreen: 60, KindRed: 400, KindGuide: 60, KindNotTested: 60, KindDeferred: 60, KindStale: 60}
	for k, w := range want {
		if got := Cap(k); got != w {
			t.Errorf("Cap(%s) = %d, want %d", k, got, w)
		}
	}
}

func TestClip_keepsATextThatFitsAndCutsOneThatDoesNot(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abcdef", 6, "abcdef"},
		{"abcdef", 7, "abcdef"},
		{"abcdef", 5, "ab…"},
		{"abcdef", 3, "…"},
		{"abcdef", 2, ""},
		{"ééé", 5, "é…"}, // 6 bytes; a cut inside the second é drops it whole
	}
	for _, c := range cases {
		if got := clip(c.in, c.n); got != c.want {
			t.Errorf("clip(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestCompose_aTextExactlyAtItsBoundIsNotCut(t *testing.T) {
	ten := strings.Repeat("a", 10)
	if got, cut := compose(100, keep("a", "", ten, "", 10)); got != ten || cut != nil {
		t.Errorf("a field at its ceiling was cut: %q %v", got, cut)
	}
	if got, cut := compose(10, flex("a", "", ten, "")); got != ten || cut != nil {
		t.Errorf("a line exactly at its limit was squeezed: %q %v", got, cut)
	}
}

func TestCompose_aFieldCutByItsBoundDoesNotSqueezeOthersThatStillFit(t *testing.T) {
	// "ab…" + " " + 20 x + the marker for rule alone (13 bytes) is 39 bytes: it fits, so cause stays whole.
	got, cut := compose(39, keep("rule", "", "abcdefgh", "", 5), flex("cause", " ", strings.Repeat("x", 20), ""))
	want := "ab… " + strings.Repeat("x", 20) + " · cut: rule"
	if got != want || !slices.Equal(cut, []string{"rule"}) {
		t.Errorf("got %q %v\nwant %q [rule]", got, cut, want)
	}
}

func TestCompose_aFieldCutByItsBoundAndAFlexFieldThatDoesNotFitBothAreNamed(t *testing.T) {
	// "ab…" + " " + 39 x is 45 bytes, over 40. Room = 40 - 5 - 1 - marker " · cut: rule, cause" (20) = 14: 11 x and "…".
	got, cut := compose(40, keep("rule", "", "abcdefgh", "", 5), flex("cause", " ", strings.Repeat("x", 39), ""))
	want := "ab… " + strings.Repeat("x", 11) + "… · cut: rule, cause"
	if got != want || !slices.Equal(cut, []string{"rule", "cause"}) || len(got) != 40 {
		t.Errorf("got %q (%d bytes) %v\nwant %q [rule cause]", got, len(got), cut, want)
	}
}

func TestCompose_squeezeSharesWhatFixedTextLeavesShortestFirst(t *testing.T) {
	// room = 33 - marker " · cut: a, b" (13) = 20; a (10) fits its half whole, b gets the other 10.
	got, cut := compose(33, flex("a", "", strings.Repeat("a", 10), ""), flex("b", "", strings.Repeat("b", 100), ""))
	want := strings.Repeat("a", 10) + strings.Repeat("b", 7) + "…" + " · cut: b"
	if got != want || !slices.Equal(cut, []string{"b"}) {
		t.Errorf("got %q %v\nwant %q [b]", got, cut, want)
	}
}

func TestCompose_aKeptFieldTrailCountsAgainstTheRoom(t *testing.T) {
	// room = 40 - "[abc]" (5) - the flex lead (1) - marker " · cut: b" (10) = 24: 21 x and the 3-byte "…".
	got, cut := compose(40, keep("a", "[", "abc", "]", 0), flex("b", " ", strings.Repeat("x", 100), ""))
	want := "[abc] " + strings.Repeat("x", 21) + "…" + " · cut: b"
	if got != want || !slices.Equal(cut, []string{"b"}) || len(got) != 40 {
		t.Errorf("got %q (%d bytes) %v\nwant %q [b]", got, len(got), cut, want)
	}
}

func TestCompose_fixedTextPastTheLimitIsClippedAndSaysSo(t *testing.T) {
	for _, n := range []int{100, 25} { // 25 is within a marker's length of the limit
		got, cut := compose(20, fixed(strings.Repeat("z", n)))
		if want := "zzzz… · cut: line"; got != want || len(got) != 20 || !slices.Equal(cut, []string{"line"}) {
			t.Errorf("%d bytes of fixed text: got %q (%d bytes) %v, want %q (20 bytes) [line]", n, got, len(got), cut, want)
		}
	}
}
