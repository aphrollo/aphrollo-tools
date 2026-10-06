package render

import "github.com/aphrollo/aphrollo-tools/internal/kernel"

// Untested is the line of the red→green rule firing on a code edit (§5): the unit
// and what the rule table gave for it, the rule's cause and next step. A deny says so
// and names the override the caller offers, which is the adapter's own (the rule
// table's text names the trellis verbs, and a live hook offers its own); a guide offers
// none. The line keeps to the deny cap and the guidance cap as the other lines of the
// grammar do, naming what it cut.
func Untested(d kernel.Decision, unit, override string) Line {
	if d.Outcome == kernel.OutcomeDeny {
		text, cut := compose(CapDeny*4,
			fixed("red-green blocked"), keep("unit", " (", plain(unit), ")", unitCeil), fixed(":"),
			flex("cause", " ", plain(d.Cause), ""),
			flex("next", " · do: ", plain(d.Next), ""),
			keep("override", " · override: ", plain(override), "", overrideCeil))
		return line(KindDeny, text, cut, d.Rule, unit)
	}
	text, cut := compose(CapGuide*4,
		fixed("red-green"), keep("unit", " (", plain(unit), ")", unitCeil), fixed(":"),
		flex("cause", " ", plain(d.Cause), ""),
		flex("next", " · do: ", plain(d.Next), ""))
	return line(KindGuide, text, cut, d.Rule, unit)
}
