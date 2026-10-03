package cli

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/guardrail"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// guardrailPolicy names the rule set in the deny counts and on the decision.
const guardrailPolicy = "guardrail"

// guardrailDecision judges a PreToolUse payload by the guardrail rules, in
// the gate's own Decision shape. A payload the rules cannot read is allowed:
// the gate's other checks have their own view of it, and a parse error must
// never wedge the session.
func guardrailDecision(raw []byte) tdd.Decision {
	d, err := guardrail.DecideFromHookInput(raw)
	if err != nil {
		return tdd.Decision{}
	}
	switch d.Action {
	case guardrail.Block:
		return tdd.Decision{Action: tdd.Block, Reason: guardrailPolicy + ": " + d.Reason, Policy: guardrailPolicy}
	case guardrail.Warn:
		return tdd.Decision{Action: tdd.Warn, Reason: guardrailPolicy + ": " + d.Reason, Policy: guardrailPolicy}
	default:
		return tdd.Decision{}
	}
}

// mergeGuardrail folds the guardrail verdict g and the gate's own verdict into
// the one the hook renders. The stronger action wins, so a guardrail warn never
// softens a gate block and never becomes a deny of its own; when both say
// something, both reasons are shown, the guardrail's first. A block the
// guardrail made offers no waiver, since the gate's override would not lift it.
func mergeGuardrail(g, gate tdd.Decision) tdd.Decision {
	switch {
	case g.Action == tdd.Allow:
		return gate
	case gate.Action == tdd.Allow:
		return g
	}
	merged := gate
	if g.Action >= gate.Action {
		merged.Action = g.Action
		merged.Policy = g.Policy
	}
	if g.Action == tdd.Block {
		merged.Override = ""
	}
	merged.Reason = strings.TrimRight(g.Reason, "\n") + "\n" + gate.Reason
	return merged
}
