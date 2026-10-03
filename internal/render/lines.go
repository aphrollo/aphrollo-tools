package render

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// Line is one rendered output: plain text, one line except the red block.
type Line struct {
	Kind Kind
	Text string
	// Cut names what had to be cut to fit the cap; the text says so too.
	Cut []string
	// ID names the fact the line says, for the seen rule (see Deliver, Due).
	ID string
}

// Tokens is the line's size in tokens.
func (l Line) Tokens() int { return Tokens(len(l.Text)) }

// Run is what a result line is about: a run's verdict for a unit at a tree,
// as the adapter read it from the run.result event and the run's output.
type Run struct {
	Unit, Test string
	Job        string // the run's job id; names it for `trellis output`
	Tree       string // the worktree key the run measured
	Verdict    kernel.Verdict
	Passed, Ms int    // green: tests passed, run time in milliseconds; 0 omits
	Assertion  string // red: the first failing assertion, as many lines as the run printed
	Cause      string // not-tested and red-bogus: why
	LastReal   kernel.Verdict
	LastTree   string
}

const (
	maxAssertionLines = 12
	bodyLineCeil      = 160
	redHeaderBytes    = 480
	footerReserve     = 128
)

func id(kind Kind, fields ...string) string {
	sum := sha256.Sum256([]byte(string(kind) + "\x00" + strings.Join(fields, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func line(kind Kind, text string, cut []string, idFields ...string) Line {
	return Line{Kind: kind, Text: text, Cut: cut, ID: id(kind, idFields...)}
}

func (r Run) key() []string {
	return []string{string(r.Verdict), r.Unit, r.Test, r.Tree, r.Job}
}

// Deny is the line of a deny: rule, cause, next step and override, and the
// reference `trellis feedback` takes (§5). Cause, detail and next are squeezed
// when long; the rule id and the override are kept whole up to their bounds.
func Deny(d kernel.Decision, ref string) Line {
	text, cut := compose(CapDeny*4,
		fixed("trellis deny ["), keep("rule", "", plain(d.Rule), "", ruleCeil), fixed("]"),
		flex("cause", " ", plain(d.Cause), ""),
		flex("detail", " (", plain(d.Detail), ")"),
		flex("next", " · do: ", plain(d.Next), ""),
		keep("override", " · override: ", plain(d.Override), "", overrideCeil),
		keep("ref", " · wrong? trellis feedback ", plain(ref), "", refCeil))
	return line(KindDeny, text, cut, d.Rule, d.Detail, ref)
}

// Guide is the line of a guide: the deny's shape without an override, which a
// guide has no use for.
func Guide(d kernel.Decision) Line {
	text, cut := compose(CapGuide*4,
		fixed("trellis guide ["), keep("rule", "", plain(d.Rule), "", ruleCeil), fixed("]"),
		flex("cause", " ", plain(d.Cause), ""),
		flex("detail", " (", plain(d.Detail), ")"),
		flex("next", " · do: ", plain(d.Next), ""))
	return line(KindGuide, text, cut, d.Rule, d.Cause, d.Detail)
}

// Decision renders what the rule table decided: nothing for an allow, a deny
// line for a deny, and a guide for anything else, an ask included: render
// never blocks on a guess.
func Decision(d kernel.Decision, ref string) Line {
	switch d.Outcome {
	case kernel.OutcomeAllow:
		return Line{}
	case kernel.OutcomeDeny:
		return Deny(d, ref)
	}
	return Guide(d)
}

// duration is a run time as "840ms" or "2.1s", empty for none.
func duration(ms int) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return strconv.Itoa(ms) + "ms"
	}
	return strconv.Itoa(ms/1000) + "." + strconv.Itoa(ms%1000/100) + "s"
}

// Green is the line of a green run (§5, cap 60 tokens).
func Green(r Run) Line {
	var facts []string
	if r.Passed > 0 {
		facts = append(facts, strconv.Itoa(r.Passed)+" passed")
	}
	if d := duration(r.Ms); d != "" {
		facts = append(facts, d)
	}
	counts := ""
	if len(facts) > 0 {
		counts = " (" + strings.Join(facts, ", ") + ")"
	}
	text, cut := compose(CapGreen*4, fixed("trellis: green"), keep("unit", " ", plain(r.Unit), "", unitCeil),
		fixed(counts+" · next: commit, or the next failing test"))
	return line(KindGreen, text, cut, r.key()...)
}

// Deferred is the line of a run that is on its way: pending, not untested
// (§6 "Tiers").
func Deferred(r Run) Line {
	run, in := fixed(" · run queued"), ""
	if j := plain(r.Job); j != "" {
		run = keep("job", " · run ", j, " queued", jobCeil)
	}
	if u := clip(plain(r.Unit), unitCeil); u != "" {
		in = " in " + u
	}
	text, cut := compose(CapGuide*4, fixed("trellis: pending"), keep("unit", " ", plain(r.Unit), "", unitCeil),
		flex("test", " ", plain(r.Test), ""), run, fixed(" · code edits stay open"+in+" meanwhile"))
	return line(KindDeferred, text, cut, r.key()...)
}

// lastReal is "green @a1b2": the last real verdict the unit stands on, or none.
func lastReal(r Run) string {
	if r.LastReal != kernel.VerdictGreen && r.LastReal != kernel.VerdictRed {
		return "none"
	}
	if t := prefix(plain(r.LastTree), 4); t != "" {
		return string(r.LastReal) + " @" + t
	}
	return string(r.LastReal)
}

var causeWords = map[string]string{
	kernel.CauseTimeout:       "timed out at the cap",
	kernel.CauseSkipped:       "skipped",
	kernel.CauseQueuedSkipped: "skipped while queued",
	kernel.CauseInfra:         "infrastructure failed",
}

// NotTested is the line of a run that did not test: the cause is named and the
// last real verdict stands (§3 "Verdict", §6).
func NotTested(r Run) Line {
	cause := plain(r.Cause)
	if w, ok := causeWords[cause]; ok {
		cause = w
	}
	if cause == "" {
		cause = "cause not recorded"
	}
	text, cut := compose(CapGuide*4, fixed("trellis: not tested"), keep("unit", " ", plain(r.Unit), "", unitCeil),
		flex("cause", " · ", cause, ""), fixed(" · last real: "+lastReal(r)))
	return line(KindNotTested, text, cut, r.key()...)
}

// Stale is the line of a verdict for a tree that has moved on (§6 "Caching",
// #813): delivered, labelled, and applied to nothing.
func Stale(r Run) Line {
	tree := ""
	if t := prefix(plain(r.Tree), 4); t != "" {
		tree = " @" + t
	}
	text, cut := compose(CapGuide*4, fixed("trellis: stale"), keep("unit", " ", plain(r.Unit), "", unitCeil),
		keep("verdict", " ", plain(string(r.Verdict)), "", 24), fixed(tree+" · the tree moved on; this run is not applied · last real: "+lastReal(r)))
	return line(KindStale, text, cut, r.key()...)
}

func isRed(v kernel.Verdict) bool {
	return v == kernel.VerdictRed || v == kernel.VerdictRedMissingImpl || v == kernel.VerdictRedBogus
}

func noun(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// Red is the line of a red run (§5, cap 400 tokens): the header names the unit
// and test and carries the first line of the failing assertion; up to eleven
// more follow indented. What does not fit is named in a last line that points
// at `trellis output <job>`. A bogus red is not a red: it names the broken
// setup instead of the code edits it opens.
func Red(r Run) Line {
	inUnit := ""
	if u := clip(plain(r.Unit), unitCeil); u != "" {
		inUnit = " in " + u
	}
	word, what, source, tail := "red", "assertion", r.Assertion, " · code edits open"+inUnit+" until it is green"
	if !isRed(r.Verdict) {
		r.Verdict = kernel.VerdictRed
	}
	if r.Verdict == kernel.VerdictRedBogus {
		what, source, tail = "cause", r.Cause, " · fix the setup; this is not a red"
		if strings.TrimSpace(source) == "" {
			source = r.Assertion
		}
	}
	if r.Verdict != kernel.VerdictRed {
		word = string(r.Verdict)
	}
	var lines []string
	for l := range strings.SplitSeq(source, "\n") {
		if l = plain(l); l != "" {
			lines = append(lines, l)
		}
	}
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}
	header, cut := compose(redHeaderBytes, fixed("trellis: "+word), keep("unit", " ", plain(r.Unit), "", unitCeil),
		flex("test", " ", plain(r.Test), ""), flex(what, " · ", first, ""), fixed(tail))
	out, room, shown, clipped := header, CapRed*4-len(header)-footerReserve, min(len(lines), 1), 0
	for i := 1; i < min(len(lines), maxAssertionLines); i++ {
		l := clip(lines[i], bodyLineCeil)
		if room < len(l)+3 {
			break
		}
		out, room, shown = out+"\n  "+l, room-len(l)-3, shown+1
		if l != lines[i] {
			clipped++
		}
	}
	if omitted := len(lines) - shown; omitted > 0 || clipped > 0 {
		var parts []string
		if omitted > 0 {
			parts = append(parts, noun(omitted, "more line"))
		}
		if clipped > 0 {
			parts = append(parts, noun(clipped, "line")+" clipped")
		}
		out += "\n  cut: " + strings.Join(parts, ", ")
		if j := clip(plain(r.Job), jobCeil); j != "" {
			out += " · full run: trellis output " + j
		}
		if !slices.Contains(cut, what) {
			cut = append(cut, what)
		}
	}
	return line(KindRed, out, cut, r.key()...)
}

// Result renders a run by its verdict: green, any red, a deferred run as
// pending, and everything else (a bogus value, a value a newer binary wrote)
// as a run that did not test.
func Result(r Run) Line {
	switch {
	case r.Verdict == kernel.VerdictGreen:
		return Green(r)
	case isRed(r.Verdict):
		return Red(r)
	case r.Cause == kernel.CauseDeferred:
		return Deferred(r)
	}
	return NotTested(r)
}

// effectGuides are the lines of the TDD machine's guide codes that no rule
// row words: the rule, the cause and the next step. The unit or test the
// guide is about rides in the detail.
var effectGuides = map[string]struct{ rule, cause, next string }{
	kernel.GuideUntestedCode: {"red-green", "a code edit that is not tested code, with no open red", "write the failing test first, then edit the code"},
	kernel.GuideHeld:         {"escape-hold", "a trunk escape holds this unit", "reproduce it with a failing test, which releases the hold"},
	kernel.GuidePassedAtOnce: {"run-result", "the test passed at once; not a red", "make it fail for the right reason before the code"},
	kernel.GuideFlaky:        {"run-result", "green and red at one unchanged tree", "treat both verdicts as unproven and fix the flake"},
}

// Effect renders a guide effect of the TDD machine (§3): the run results as
// their lines, the rest as guides. Any other effect, and a guide code this
// build does not know, renders nothing.
func Effect(fx kernel.Effect) Line {
	if fx.Kind != kernel.EffectGuide {
		return Line{}
	}
	r := Run{Unit: fx.Unit, Test: fx.Test, Cause: fx.Cause, Tree: fx.Tree}
	switch fx.Detail {
	case kernel.GuidePending:
		return Deferred(r)
	case kernel.GuideNotTested:
		return NotTested(r)
	case kernel.GuideStale:
		return Stale(r)
	case kernel.GuideRedBogus:
		r.Verdict = kernel.VerdictRedBogus
		return Red(r)
	}
	g, ok := effectGuides[fx.Detail]
	if !ok {
		return Line{}
	}
	about := fx.Unit
	if fx.Detail == kernel.GuidePassedAtOnce {
		about = fx.Test
	}
	return Guide(kernel.Decision{Outcome: kernel.OutcomeGuide, Rule: g.rule, Cause: g.cause, Next: g.next, Detail: about})
}
