package report

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// textRows is how many rows of a table the text shows; the rest is counted and
// the whole table is in --json.
const textRows = 15

// Text is the report as the plain text an issue body and the terminal carry.
// Same report, same bytes.
func (r Report) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	more := func(shown, total int) {
		if total > shown {
			p("  ... %d more rows (--json lists them all)", total-shown)
		}
	}
	p("%s: %s", r.Title, r.Repo)
	p("%s up to %s, %d events. Replay any event with `aphrollo why <seq>`.", r.Window, r.Until, r.Events)
	b.WriteString(measure.VersionsText(r.Versions))

	p("")
	p("Changed")
	for _, c := range r.Changes() {
		p("  %s", c)
	}

	p("")
	r.Speed.text(p, more)
	r.Expectations.text(p)

	p("")
	p("1. Friction per rule (denies, overrides, refusals, not-tested runs, time lost)")
	for _, f := range r.Friction[:min(len(r.Friction), textRows)] {
		p("  %-44s denies %d  overrides %d  refusals %d  not tested %d  time lost %s  %s",
			f.Rule, f.Denies, f.Overrides, f.Refusals, f.NotTested, dur(f.SecsLost), f.Refs.text())
	}
	more(textRows, len(r.Friction))
	if len(r.Friction) == 0 {
		p("  none")
	}

	p("")
	p("2. Wrong-block candidates (override rate per rule, shadow would-be wrong blocks, stand-downs)")
	shown := 0
	for _, w := range r.WrongBlocks {
		if w.Waived == 0 || shown == textRows {
			continue
		}
		shown++
		p("  %-44s %d of %d denies waived (%s)  %s", w.Rule, w.Waived, w.Denies, w.Rate, w.Refs.text())
	}
	if shown == 0 {
		p("  no deny was waived by an override")
	}
	for _, s := range r.ShadowWrong {
		p("  shadow %-37s %d would-be blocks of %d fires: %d wrong, %d caught, %d open", s.Rule, s.Stricter, s.Fires, s.Wrong, s.Catches, s.Open)
	}
	for _, s := range r.Standdowns[:min(len(r.Standdowns), textRows)] {
		p("  stood down %-33s %d times  %s", s.Matcher, s.N, s.Refs.text())
	}
	more(textRows, len(r.Standdowns))

	p("")
	p("3. Escapes by class and the stage that should have caught them")
	for _, e := range r.Escapes.Rows {
		p("  %-14s %d  should have been caught by: %s  %s", e.Class, e.N, e.Caught, e.Refs.text())
	}
	if len(r.Escapes.Rows) == 0 {
		p("  none")
	}
	p("  false positives (wrong denies): %d", r.Escapes.FalsePositives)

	p("")
	p("4. A/B and shadow, per arm and language")
	b.WriteString(indent(r.AB.Text()))
	p("  cumulative (whole log): %s; %s", abLanes(r.ABTotal), abStatus(r.ABTotal))
	p("  shadow: %d fires, %d dropped for the budget", r.Shadow.Fires, r.Shadow.Dropped)
	for _, l := range r.Shadow.Languages {
		rate := l.Rate()
		if rate == "" {
			rate = "under " + strconv.Itoa(measure.MinShadowFires) + " fires"
		}
		p("    %-12s %d fires  agree %s  held out %d  budget drops %d", l.Lang, l.Fires, rate, l.HeldOut, l.Dropped)
	}

	p("")
	p("5. Token cost of what the harness injects, and the biggest gate lines")
	b.WriteString(indent(measure.BriefsText(r.Tokens.Briefs)))
	if len(r.Tokens.Briefs) == 0 {
		p("  (no briefs measured)")
	}
	for _, l := range r.Tokens.Biggest {
		p("  gate line %-40s %d lines  %d tokens  %s", l.Name, l.N, l.Tokens, l.Refs.text())
	}

	p("")
	p("6. Proposals (the report proposes; nothing is applied)")
	for _, pr := range r.Proposals {
		p("  %s: %s", pr.Rule, pr.Numbers)
		p("    propose: %s  %s", pr.Change, pr.Refs.text())
	}
	if len(r.Proposals) == 0 {
		p("  none: no rule is over a threshold")
	}
	if r.Usage != nil {
		p("")
		p("7. Session usage (aggregates only: no prompt, code, tool text or injected text is copied)")
		b.WriteString(r.Usage.Text())
	} else if r.withheld {
		p("")
		p("7. Session usage: withheld, because the undercover check refused it")
	}
	if len(r.ByVersion) > 0 {
		p("")
		p("8. By version (each lane with the version that opened it)")
		for _, v := range r.ByVersion {
			p("  %-12s %d events  denies %d  overrides %d", v.Version, v.Events, v.Denies, v.Overrides)
			b.WriteString(indent(indent(v.AB.Text())))
		}
	}
	return b.String()
}

func abStatus(ab measure.AB) string {
	if ab.Decidable {
		return "both arms have the lanes: the A/B can decide"
	}
	return "not decidable yet"
}

func (r Refs) text() string {
	if len(r.Seqs) == 0 {
		return ""
	}
	parts := make([]string, len(r.Seqs))
	for i, s := range r.Seqs {
		parts[i] = strconv.FormatInt(s, 10)
	}
	out := "seq " + strings.Join(parts, ",")
	if r.More > 0 {
		out += fmt.Sprintf(" (+%d more)", r.More)
	}
	return out
}

func dur(s float64) string {
	switch {
	case s >= 3600:
		return fmt.Sprintf("%.1fh", s/3600)
	case s >= 60:
		return fmt.Sprintf("%.1fm", s/60)
	}
	return fmt.Sprintf("%.0fs", s)
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if line == "" {
			continue
		}
		b.WriteString("  " + line)
	}
	return b.String()
}
