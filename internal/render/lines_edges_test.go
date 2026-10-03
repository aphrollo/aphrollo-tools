package render

import (
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

func TestGreen_countsAndTimesOnlyWhatItKnows(t *testing.T) {
	cases := []struct {
		run  Run
		want string
	}{
		{Run{Unit: "u"}, "trellis: green u · next: commit, or the next failing test"},
		{Run{Unit: "u", Ms: 5}, "trellis: green u (5ms) · next: commit, or the next failing test"},
		{Run{Unit: "u", Passed: 1, Ms: 999}, "trellis: green u (1 passed, 999ms) · next: commit, or the next failing test"},
		{Run{Unit: "u", Passed: 3, Ms: 1000}, "trellis: green u (3 passed, 1.0s) · next: commit, or the next failing test"},
		{Run{Unit: "u", Passed: 2, Ms: 61999}, "trellis: green u (2 passed, 61.9s) · next: commit, or the next failing test"},
	}
	for _, c := range cases {
		if got := Green(c.run).Text; got != c.want {
			t.Errorf("Green(%+v)\n got: %s\nwant: %s", c.run, got, c.want)
		}
	}
}

// redRoom is the room a red block has below its header for body lines: the
// cap, minus the header, minus what the cut footer reserves.
func redRoom(t *testing.T, first string) int {
	t.Helper()
	return CapRed*4 - len(redWithBody(first).Text) - 128
}

func redWithBody(first string, body ...string) Line {
	return Red(Run{Unit: "u", Job: "j", Verdict: kernel.VerdictRed, Assertion: strings.Join(append([]string{first}, body...), "\n")})
}

func TestRed_aBodyLineThatExactlyFillsTheRoomIsShown(t *testing.T) {
	room := redRoom(t, "f")
	body := slices.Repeat([]string{strings.Repeat("b", 150)}, 9) // 9 lines of 150 + 2 indent + 1 newline
	last := room - 9*153 - 3                                     // the tenth line fills the room to the byte
	fit := redWithBody("f", append(slices.Clone(body), strings.Repeat("c", last))...)
	if strings.Contains(fit.Text, "cut:") || strings.Count(fit.Text, "\n") != 10 {
		t.Errorf("a line that fills the room exactly was dropped:\n%s", fit.Text)
	}
	over := redWithBody("f", append(slices.Clone(body), strings.Repeat("c", last+1))...)
	if !strings.HasSuffix(over.Text, "\n  cut: 1 more line · full run: trellis output j") {
		t.Errorf("a line one byte past the room was not named as cut:\n%s", over.Text)
	}
}

func TestRed_clippedLinesAloneAreNamedWithoutInventingOmittedOnes(t *testing.T) {
	l := redWithBody("f", strings.Repeat("w", 400))
	if want := "  cut: 1 line clipped · full run: trellis output j"; !strings.HasSuffix(l.Text, want) {
		t.Errorf("footer\n got: %q\nwant suffix: %q", l.Text, want)
	}
}

func TestEffect_namesWhatEachGuideIsAbout(t *testing.T) {
	fx := kernel.Effect{Kind: kernel.EffectGuide, Unit: "internal/lane", Test: "TestX"}
	fx.Detail = kernel.GuidePassedAtOnce
	if l := Effect(fx); !strings.Contains(l.Text, "(TestX)") {
		t.Errorf("passed-at-once names %q, want the test", l.Text)
	}
	fx.Detail = kernel.GuideFlaky
	if l := Effect(fx); !strings.Contains(l.Text, "(internal/lane)") {
		t.Errorf("flaky names %q, want the unit", l.Text)
	}
}
