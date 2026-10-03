package render

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// word draws text that stresses a renderer: any unicode, newlines, escape
// bytes, and lengths from empty to well past every cap.
func word(longest int) *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.String(),
		rapid.StringN(0, longest, longest),
		rapid.SampledFrom([]string{"", "a b", "x\ny\nz", "\x1b[31mred\x1b[0m", "é·→", "tab\there"}),
		rapid.Map(rapid.IntRange(0, longest), func(n int) string { return strings.Repeat("ü", n) }),
	)
}

func genDecision() *rapid.Generator[kernel.Decision] {
	return rapid.Custom(func(t *rapid.T) kernel.Decision {
		return kernel.Decision{
			Outcome:  rapid.SampledFrom([]kernel.Outcome{kernel.OutcomeAllow, kernel.OutcomeGuide, kernel.OutcomeAsk, kernel.OutcomeDeny}).Draw(t, "outcome"),
			Rule:     word(400).Draw(t, "rule"),
			Cause:    word(3000).Draw(t, "cause"),
			Next:     word(3000).Draw(t, "next"),
			Override: word(1000).Draw(t, "override"),
			Detail:   word(3000).Draw(t, "detail"),
		}
	})
}

func genRun() *rapid.Generator[Run] {
	return rapid.Custom(func(t *rapid.T) Run {
		return Run{
			Unit: word(600).Draw(t, "unit"), Test: word(600).Draw(t, "test"), Job: word(200).Draw(t, "job"),
			Tree: word(200).Draw(t, "tree"), LastTree: word(200).Draw(t, "lastTree"),
			Verdict: rapid.SampledFrom([]kernel.Verdict{kernel.VerdictGreen, kernel.VerdictRed, kernel.VerdictRedMissingImpl,
				kernel.VerdictRedBogus, kernel.VerdictNotTested, "", "odd"}).Draw(t, "verdict"),
			LastReal:  rapid.SampledFrom([]kernel.Verdict{"", kernel.VerdictGreen, kernel.VerdictRed}).Draw(t, "lastReal"),
			Passed:    rapid.IntRange(0, 1<<30).Draw(t, "passed"),
			Ms:        rapid.IntRange(0, 1<<40).Draw(t, "ms"),
			Cause:     word(3000).Draw(t, "cause"),
			Assertion: strings.Join(rapid.SliceOfN(rapid.StringN(0, 700, 700), 0, 40).Draw(t, "lines"), "\n"),
		}
	})
}

func TestRender_nothingRendersOverItsCap(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := genDecision().Draw(t, "decision")
		ref := word(200).Draw(t, "ref")
		l := Decision(d, ref)
		checkLine(t, l)
		if again := Decision(d, ref); again.Text != l.Text || again.ID != l.ID {
			t.Fatalf("the same decision rendered two ways:\n%q\n%q", l.Text, again.Text)
		}
		if d.Outcome == kernel.OutcomeAllow && l.Text != "" {
			t.Fatalf("an allow rendered %q", l.Text)
		}
		if l.Kind == KindDeny && len(plain(d.Override)) <= overrideCeil {
			if o := plain(d.Override); o != "" && !strings.Contains(l.Text, "override: "+o) {
				t.Fatalf("a deny lost its override %q: %q", o, l.Text)
			}
		}
		if l.Kind == KindDeny && len(plain(d.Rule)) <= ruleCeil && !strings.Contains(l.Text, "["+plain(d.Rule)+"]") {
			t.Fatalf("a deny lost its rule id %q: %q", d.Rule, l.Text)
		}

		r := genRun().Draw(t, "run")
		for _, rl := range []Line{Result(r), Green(r), Red(r), NotTested(r), Deferred(r), Stale(r)} {
			checkLine(t, rl)
		}
		// Red never carries more than twelve assertion lines.
		if rl := Red(r); strings.Count(rl.Text, "\n") > 12 {
			t.Fatalf("red block has %d lines", strings.Count(rl.Text, "\n")+1)
		}
	})
}

func TestEnvelopes_nothingRendersOverItsCap(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		text := word(20000).Draw(t, "text")
		caps := map[Hook]int{HookPreToolUse: CapGuide, HookSubagentStart: CapSubagentBrief, HookSessionStart: CapBrief}
		for _, h := range []Hook{HookPreToolUse, HookPostToolUse, HookPostToolBatch, HookSubagentStart, HookUserPromptSubmit, HookSessionStart} {
			b := Context(h, text)
			if b == nil {
				continue
			}
			var v struct {
				Out struct {
					Text string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatalf("%s: %v: %s", h, err, b)
			}
			if !utf8.ValidString(v.Out.Text) {
				t.Fatalf("%s clipped inside a character", h)
			}
			limit, ok := caps[h]
			if !ok {
				if len(v.Out.Text) > PlatformContextBytes {
					t.Fatalf("%s context is %d bytes, over the platform's %d", h, len(v.Out.Text), PlatformContextBytes)
				}
				continue
			}
			if got := Tokens(len(v.Out.Text)); got > limit {
				t.Fatalf("%s context is %d tokens, cap %d", h, got, limit)
			}
		}
		for _, b := range [][]byte{PreToolUseDeny(text), StopBlock(text)} {
			var v map[string]any
			if b != nil && json.Unmarshal(b, &v) != nil {
				t.Fatalf("not JSON: %s", b)
			}
		}
	})
}

// A rule table text that does not fit its cap would be cut on every fire. Run
// the real table through the renderer: no real decision may need a cut.
func TestDecision_noRealKernelDecisionNeedsACut(t *testing.T) {
	kinds := []kernel.Kind{kernel.KindPreTool, kernel.KindPreCommit, kernel.KindPrePush, kernel.KindPreMerge, kernel.KindStop, kernel.KindRunResult}
	rapid.Check(t, func(t *rapid.T) {
		var cmds kernel.Cmd
		for _, b := range []kernel.Cmd{kernel.CmdWrite, kernel.CmdBypassGate, kernel.CmdMoveOffTrunk, kernel.CmdPushTrunk, kernel.CmdMergePR,
			kernel.CmdDiscard, kernel.CmdOutward, kernel.CmdLongWait, kernel.CmdRerun, kernel.CmdNoisy} {
			if rapid.Bool().Draw(t, "cmd") {
				cmds |= b
			}
		}
		e := kernel.Event{
			Kind: rapid.SampledFrom(kinds).Draw(t, "kind"), Lane: rapid.SampledFrom([]string{"", kernel.TrunkLane, "fix"}).Draw(t, "lane"),
			Claude: true, Tool: rapid.SampledFrom([]kernel.Tool{kernel.ToolWrite, kernel.ToolBash}).Draw(t, "tool"),
			Target: rapid.SampledFrom([]kernel.PathClass{kernel.PathLane, kernel.PathPrimary}).Draw(t, "target"), Cmds: cmds,
			NonMerge: rapid.Bool().Draw(t, "nonMerge"), Secret: rapid.Bool().Draw(t, "secret"), Attribution: rapid.Bool().Draw(t, "attr"),
			LawHit: rapid.SampledFrom([]string{"", "no-panic"}).Draw(t, "law"), LawDeny: rapid.Bool().Draw(t, "lawDeny"),
			Failed:    rapid.SampledFrom([]kernel.Check{"", kernel.CheckProof, kernel.CheckVet, kernel.CheckLint, kernel.CheckDocs}).Draw(t, "failed"),
			Survivors: rapid.IntRange(0, 2).Draw(t, "survivors"), UnseenRed: rapid.Bool().Draw(t, "unseen"),
			Verdict: rapid.SampledFrom([]kernel.Verdict{kernel.VerdictRedBogus, kernel.VerdictNotTested}).Draw(t, "verdict"),
		}
		cfg := kernel.Config{TDD: kernel.ModeEnforce, Undercover: true, Mutation: kernel.LevelEnforce}
		d := kernel.Decide(kernel.State{Branch: e.Lane}, kernel.Units{"pkg": {Phase: kernel.PhaseOpen}}, e, cfg)
		l := Decision(d, "d-7f3a")
		checkLine(t, l)
		if len(l.Cut) != 0 {
			t.Fatalf("rule %q needed a cut: %v: %q", d.Rule, l.Cut, l.Text)
		}
	})
}
