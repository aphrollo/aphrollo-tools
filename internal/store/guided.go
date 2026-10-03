package store

import (
	"maps"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// Flags are a unit's guided-once flags (kernel.Unit.GuidedUntested and
// GuidedHeld) kept apart from the unit: the one guidance line per unit per lane
// was given. A question stores them in Record.Guided rather than as a Units
// entry, which would be a unit no fact has named: a unit-less gated commit
// would seed its last real verdict, and a question would have changed what the
// facts say. The first fact that names the unit moves its flags onto the unit's
// entry and removes them from Guided.
type Flags struct{ Untested, Held bool }

// DecideUnits is the units a decision about unit reads: the record's, with the
// flags kept aside for that unit put on it. The other flagged units stay out,
// so a unit-less fact never sees a unit no fact has named.
func (r Record) DecideUnits(unit string) kernel.Units {
	f, ok := r.Guided[unit]
	if !ok {
		return r.Units
	}
	out := maps.Clone(r.Units)
	if out == nil {
		out = kernel.Units{}
	}
	u := out[unit]
	u.GuidedUntested, u.GuidedHeld = u.GuidedUntested || f.Untested, u.GuidedHeld || f.Held
	out[unit] = u
	return out
}

// Settled is Guided once a fact has named unit: its flags now live on the
// unit's entry.
func (r Record) Settled(unit string) map[string]Flags {
	if _, ok := r.Guided[unit]; !ok {
		return r.Guided
	}
	out := maps.Clone(r.Guided)
	delete(out, unit)
	if len(out) == 0 {
		return nil
	}
	return out
}
