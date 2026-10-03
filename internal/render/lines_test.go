package render

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

func golden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile("testdata/" + name + ".golden")
	if err != nil {
		t.Fatalf("golden %s: %v", name, err)
	}
	if got != string(want) {
		t.Errorf("%s drifted from its golden\n got: %q\nwant: %q", name, got, want)
	}
}

// checkLine holds what every rendered line owes the agent, whatever its input:
// inside its cap measured as bytes÷4, valid UTF-8, plain text, and one line
// unless it is the red block.
func checkLine(t reporter, l Line) {
	t.Helper()
	if got, limit := l.Tokens(), Cap(l.Kind); got > limit {
		t.Errorf("%s line is %d tokens, cap %d: %q", l.Kind, got, limit, l.Text)
	}
	if !utf8.ValidString(l.Text) {
		t.Errorf("%s line is not valid UTF-8: %q", l.Kind, l.Text)
	}
	if strings.ContainsRune(l.Text, '\x1b') || strings.ContainsAny(l.Text, "\r\t") {
		t.Errorf("%s line carries a control character: %q", l.Kind, l.Text)
	}
	if l.Kind != KindRed && strings.Contains(l.Text, "\n") {
		t.Errorf("%s line spans lines: %q", l.Kind, l.Text)
	}
	if len(l.Cut) > 0 && !strings.Contains(l.Text, "cut:") {
		t.Errorf("%s line cut %v without saying so: %q", l.Kind, l.Cut, l.Text)
	}
}

func denyDecision() kernel.Decision {
	return kernel.Decision{
		Outcome: kernel.OutcomeDeny, Rule: "primary-write",
		Cause:    "Write lands in the main checkout on trunk",
		Next:     "EnterWorktree name=fix-parse",
		Override: "trellis allow primary-write --once",
	}
}

func TestLines_matchGolden(t *testing.T) {
	guide := kernel.Decision{
		Outcome: kernel.OutcomeGuide, Rule: "warn-law",
		Cause: "an edit or commit trips a warn law", Next: "fix the finding the law names",
		Detail: "no-panic", Override: "never shown on a guide",
	}
	cases := []struct {
		name string
		kind Kind
		line Line
	}{
		{"green", KindGreen, Green(Run{Unit: "internal/lane", Passed: 14, Ms: 2100})},
		{"deferred", KindDeferred, Deferred(Run{Unit: "internal/lane", Test: "TestOpenOnRed", Job: "j42"})},
		{"red", KindRed, Red(Run{Unit: "internal/lane", Test: "TestOpenOnRed", Verdict: kernel.VerdictRed,
			Assertion: "lane_test.go:41: want open, got closed"})},
		{"red_block", KindRed, Red(Run{Unit: "internal/lane", Test: "TestOpenOnRed", Verdict: kernel.VerdictRed,
			Assertion: "--- FAIL: TestOpenOnRed (0.00s)\n    lane_test.go:41: want open, got closed\nFAIL\n"})},
		{"red_bogus", KindRed, Red(Run{Unit: "internal/store", Verdict: kernel.VerdictRedBogus,
			Cause: "build failed: store.go:88: undefined: lockPath"})},
		{"not_tested", KindNotTested, NotTested(Run{Unit: "internal/run", Cause: "deps installing in lane fix-parse",
			LastReal: kernel.VerdictGreen, LastTree: "a1b2c3d4"})},
		{"stale", KindStale, Stale(Run{Unit: "internal/lane", Verdict: kernel.VerdictGreen, Tree: "9f2c77",
			LastReal: kernel.VerdictRed, LastTree: "a1b2c3d4"})},
		{"deny", KindDeny, Deny(denyDecision(), "d-7f3a")},
		{"guide", KindGuide, Guide(guide)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.line.Kind != c.kind {
				t.Errorf("kind = %q, want %q", c.line.Kind, c.kind)
			}
			golden(t, c.name, c.line.Text)
			checkLine(t, c.line)
			if len(c.line.Cut) != 0 {
				t.Errorf("a short input was cut: %v", c.line.Cut)
			}
		})
	}
}

func TestDeny_keepsRuleAndOverrideWhenTheCauseIsCut(t *testing.T) {
	d := denyDecision()
	d.Cause = strings.Repeat("a long cause ", 200)
	l := Deny(d, "d-7f3a")
	checkLine(t, l)
	if !slices.Equal(l.Cut, []string{"cause"}) {
		t.Fatalf("Cut = %v, want [cause]", l.Cut)
	}
	for _, keep := range []string{"[primary-write]", "do: EnterWorktree name=fix-parse",
		"override: trellis allow primary-write --once", "trellis feedback d-7f3a", "· cut: cause"} {
		if !strings.Contains(l.Text, keep) {
			t.Errorf("a cut cause lost %q: %q", keep, l.Text)
		}
	}
}

func TestDeny_namesARuleIdItHadToCut(t *testing.T) {
	d := denyDecision()
	d.Rule = strings.Repeat("r", 200)
	l := Deny(d, "")
	checkLine(t, l)
	if !slices.Contains(l.Cut, "rule") {
		t.Errorf("a 200-byte rule id was cut without naming it: Cut = %v, text %q", l.Cut, l.Text)
	}
}

func TestRed_capsTheAssertionAtTwelveLinesAndNamesTheRest(t *testing.T) {
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, "assertion line "+strings.Repeat("x", i))
	}
	l := Red(Run{Unit: "internal/lane", Test: "TestOpenOnRed", Job: "j42", Verdict: kernel.VerdictRed,
		Assertion: strings.Join(lines, "\n")})
	checkLine(t, l)
	body := strings.Split(l.Text, "\n")
	// one header, eleven continuation lines, one footer
	if len(body) != 13 {
		t.Fatalf("red block has %d lines, want 13 (header + 11 + footer):\n%s", len(body), l.Text)
	}
	if want := "  cut: 18 more lines · full run: trellis output j42"; body[12] != want {
		t.Errorf("footer = %q, want %q", body[12], want)
	}
	if !slices.Equal(l.Cut, []string{"assertion"}) {
		t.Errorf("Cut = %v, want [assertion]", l.Cut)
	}
}

func TestRed_staysInItsCapWhenEveryAssertionLineIsLong(t *testing.T) {
	var lines []string
	for range 12 {
		lines = append(lines, strings.Repeat("w", 400))
	}
	l := Red(Run{Unit: "u", Test: "T", Job: "j1", Verdict: kernel.VerdictRed, Assertion: strings.Join(lines, "\n")})
	checkLine(t, l)
	if !strings.Contains(l.Text, "lines clipped") {
		t.Errorf("clipped assertion lines were not named: %q", l.Text)
	}
}

func TestLines_areOneLinePlainText(t *testing.T) {
	d := denyDecision()
	d.Cause = "\x1b[31mred\x1b[0m cause\nsecond\tline\r"
	l := Deny(d, "d-1")
	checkLine(t, l)
	if !strings.Contains(l.Text, "red cause second line") {
		t.Errorf("control characters were not flattened to single spaces: %q", l.Text)
	}
}

func TestResult_picksTheLineByVerdict(t *testing.T) {
	cases := []struct {
		name string
		run  Run
		want Kind
	}{
		{"green", Run{Verdict: kernel.VerdictGreen}, KindGreen},
		{"red", Run{Verdict: kernel.VerdictRed}, KindRed},
		{"red missing impl", Run{Verdict: kernel.VerdictRedMissingImpl}, KindRed},
		{"bogus", Run{Verdict: kernel.VerdictRedBogus}, KindRed},
		{"not tested", Run{Verdict: kernel.VerdictNotTested, Cause: kernel.CauseTimeout}, KindNotTested},
		{"deferred is pending, not untested", Run{Verdict: kernel.VerdictNotTested, Cause: kernel.CauseDeferred}, KindDeferred},
		{"a value a newer binary wrote did not test", Run{Verdict: "from-the-future"}, KindNotTested},
	}
	for _, c := range cases {
		if got := Result(c.run).Kind; got != c.want {
			t.Errorf("%s: kind = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRed_missingImplKeepsItsOwnWord(t *testing.T) {
	l := Red(Run{Unit: "u", Verdict: kernel.VerdictRedMissingImpl, Assertion: "undefined: Foo"})
	if !strings.HasPrefix(l.Text, "trellis: red-missing-impl u ") {
		t.Errorf("the clean RED lost its name: %q", l.Text)
	}
}

func TestDecision_rendersByOutcome(t *testing.T) {
	if l := Decision(kernel.Decision{Outcome: kernel.OutcomeAllow}, ""); l.Text != "" || l.Kind != "" {
		t.Errorf("an allow rendered %+v, want nothing", l)
	}
	guide := Decision(kernel.Decision{Outcome: kernel.OutcomeGuide, Rule: "lint", Cause: "lint failed"}, "d-1")
	if guide.Kind != KindGuide || strings.Contains(guide.Text, "feedback") {
		t.Errorf("a guide rendered as %q %q", guide.Kind, guide.Text)
	}
	ask := Decision(kernel.Decision{Outcome: kernel.OutcomeAsk, Rule: "lint", Cause: "lint failed"}, "d-1")
	if ask.Kind != KindGuide {
		t.Errorf("an ask rendered as %q, want a guide: the table never asks, and render never blocks on a guess", ask.Kind)
	}
	if deny := Decision(denyDecision(), "d-1"); deny.Kind != KindDeny {
		t.Errorf("a deny rendered as %q", deny.Kind)
	}
}

func TestDecision_realKernelDenyNamesRuleAndOverrideUncut(t *testing.T) {
	d := kernel.Decide(kernel.State{Branch: kernel.TrunkLane}, nil, kernel.Event{
		Kind: kernel.KindPreTool, Lane: kernel.TrunkLane, Claude: true,
		Tool: kernel.ToolWrite, Target: kernel.PathPrimary,
	}, kernel.Config{})
	if d.Outcome != kernel.OutcomeDeny {
		t.Fatalf("Decide gave %q, want a deny of the primary-write wall", d.Outcome)
	}
	l := Decision(d, "d-9")
	checkLine(t, l)
	for _, want := range []string{"[primary-write]", "override: trellis allow primary-write --once", "wrong? trellis feedback d-9"} {
		if !strings.Contains(l.Text, want) {
			t.Errorf("the real primary-write deny lost %q: %q", want, l.Text)
		}
	}
	if len(l.Cut) != 0 {
		t.Errorf("the kernel's own table text had to be cut: %v", l.Cut)
	}
}

func TestEffect_mapsGuideCodesToTheirLines(t *testing.T) {
	cases := []struct {
		code string
		kind Kind
	}{
		{kernel.GuidePending, KindDeferred},
		{kernel.GuideNotTested, KindNotTested},
		{kernel.GuideStale, KindStale},
		{kernel.GuideRedBogus, KindRed},
		{kernel.GuideUntestedCode, KindGuide},
		{kernel.GuideHeld, KindGuide},
		{kernel.GuidePassedAtOnce, KindGuide},
		{kernel.GuideFlaky, KindGuide},
	}
	for _, c := range cases {
		l := Effect(kernel.Effect{Kind: kernel.EffectGuide, Detail: c.code, Unit: "internal/lane", Test: "TestX", Cause: "timeout"})
		if l.Kind != c.kind {
			t.Errorf("guide %q rendered as %q, want %q", c.code, l.Kind, c.kind)
		}
		checkLine(t, l)
	}
	if l := Effect(kernel.Effect{Kind: kernel.EffectGuide, Detail: "a-code-from-a-newer-kernel"}); l.Text != "" {
		t.Errorf("an unknown guide code rendered %q, want silence", l.Text)
	}
	if l := Effect(kernel.Effect{Kind: kernel.EffectRequestRun}); l.Text != "" {
		t.Errorf("a non-guide effect rendered %q, want silence", l.Text)
	}
}

func TestNotTested_namesTheCauseAndNeverReadsAsGreen(t *testing.T) {
	l := NotTested(Run{Unit: "u", Cause: kernel.CauseTimeout})
	if strings.Contains(l.Text, "green") || !strings.Contains(l.Text, "timed out") || !strings.Contains(l.Text, "last real: none") {
		t.Errorf("a timeout with no earlier verdict read as %q", l.Text)
	}
}

// reporter is what *testing.T and *rapid.T share.
type reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// cutCases are inputs far past their caps. Their goldens were read through
// once by hand: each is inside its cap, ends in the cut it names, and keeps
// the rule id, the override and the job a reader needs.
func cutCases() map[string]Line {
	d := denyDecision()
	d.Cause = strings.Repeat("the write lands in the main checkout ", 40)
	d.Detail = strings.Repeat("detail ", 80)
	var asserts []string
	for i := 1; i <= 14; i++ {
		asserts = append(asserts, fmt.Sprintf("    lane_test.go:%d: want open, got closed; the lane was %s", i, strings.Repeat("x", i*14)))
	}
	return map[string]Line{
		"deny_cut": Deny(d, "d-7f3a"),
		"guide_cut": Guide(kernel.Decision{Rule: "warn-law", Cause: strings.Repeat("cause ", 80),
			Next: strings.Repeat("next ", 80), Detail: "no-panic"}),
		"red_cut": Red(Run{Unit: "internal/lane", Test: "TestOpenOnRed", Job: "j42", Verdict: kernel.VerdictRed,
			Assertion: strings.Join(asserts, "\n")}),
	}
}

func TestLines_pastTheirCapMatchGolden(t *testing.T) {
	for name, l := range cutCases() {
		t.Run(name, func(t *testing.T) {
			golden(t, name, l.Text)
			checkLine(t, l)
			if len(l.Cut) == 0 {
				t.Error("an input far past its cap was not reported as cut")
			}
		})
	}
}
