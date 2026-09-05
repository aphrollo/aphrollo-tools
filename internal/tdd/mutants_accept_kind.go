package tdd

import "regexp"

// A mutation-accept entry conflated two different claims under one mechanism
// (issue #268), and only one of them is genuinely debt:
//
//   - equivalent — the mutant computes the same thing. Closed permanently:
//     nothing to revisit, and a wrong claim here hides a real bug forever.
//   - unobservable-runner — a covering test genuinely exists, in a tier this
//     runner does not execute. Bounded and verifiable TODAY by naming the
//     test: a statement about SCOPE.
//   - unobservable-capability — no covering test can exist, because nothing
//     can read what the code produces. Open-ended, verifiable only by an
//     argument about an upstream API: a statement about CAPABILITY, and the
//     only one of the three that is genuinely parked work.
//
// Collapsing the last two lets the capability kind go stale silently: when
// the blocker lifts, nothing notices the entry became killable, because it
// reads exactly like the entries that never will be.
type acceptKind int

const (
	// acceptKindEquivalent is also the zero value, so an entry with no
	// "kind=" directive at all — every entry that predates this type —
	// reads as equivalent, matching this file's own historical behaviour
	// and the accept-list's own header claim. Never an error.
	acceptKindEquivalent acceptKind = iota
	acceptKindUnobservableRunner
	acceptKindUnobservableCapability
)

// The literal spellings a "kind=" directive accepts. Anything else is a
// misspelling, refused loudly rather than silently read as equivalent.
const (
	acceptKindEquivalentText             = "equivalent"
	acceptKindUnobservableRunnerText     = "unobservable-runner"
	acceptKindUnobservableCapabilityText = "unobservable-capability"
)

// acceptKindDirectiveRe matches the optional machine-readable prefix at the
// front of an accept-list entry's reason:
//
//	kind=<kind>[ test=<name>|issue=<ref>]: <free text>
//
// group 1 is the kind literal, group 2 is "test" or "issue" (empty when no
// evidence field is present), group 3 is the evidence value.
var acceptKindDirectiveRe = regexp.MustCompile(`^kind=(\S+?)(?:\s+(test|issue)=(\S+))?\s*:`)

// parseAcceptKind reads the optional directive at the front of reason
// (already trimmed of leading/trailing space). ok is false ONLY when a
// "kind=" prefix was present and malformed or misspelled — a reason with no
// such prefix at all is legacy shorthand for equivalent, never an error.
//
// What is enforced: the kind literal must be one of the three spellings
// above, unobservable-runner must carry a non-empty test= evidence field,
// and unobservable-capability must carry a non-empty issue= evidence field.
// What is NOT enforced: that the named test actually exists and actually
// covers this mutant, or that the named issue actually tracks the claimed
// blocker — verifying either requires reading outside this file, and stays a
// review question.
func parseAcceptKind(reason string) (kind acceptKind, evidence string, ok bool) {
	m := acceptKindDirectiveRe.FindStringSubmatch(reason)
	if m == nil {
		if len(reason) >= 5 && reason[:5] == "kind=" {
			return acceptKindEquivalent, "", false
		}
		return acceptKindEquivalent, "", true
	}
	kindText, evidenceKey, evidenceVal := m[1], m[2], m[3]
	switch kindText {
	case acceptKindEquivalentText:
		return acceptKindEquivalent, "", true
	case acceptKindUnobservableRunnerText:
		if evidenceKey != "test" || evidenceVal == "" {
			return acceptKindEquivalent, "", false
		}
		return acceptKindUnobservableRunner, evidenceVal, true
	case acceptKindUnobservableCapabilityText:
		if evidenceKey != "issue" || evidenceVal == "" {
			return acceptKindEquivalent, "", false
		}
		return acceptKindUnobservableCapability, evidenceVal, true
	default:
		return acceptKindEquivalent, "", false
	}
}

// acceptEntry is one parsed mutation-accept line: the survivor key it
// matches and the claim its reason makes.
type acceptEntry struct {
	Kind     acceptKind
	Evidence string
}

// AcceptKindCounts splits MutationReceipt.Accepted by the CLAIM each matched
// entry makes, so a reader — and a merge gate — can see how much of the
// accepted set is closed for good versus parked (issue #268).
type AcceptKindCounts struct {
	AcceptedEquivalent             int `json:"accepted_equivalent,omitempty"`
	AcceptedUnobservableRunner     int `json:"accepted_unobservable_runner,omitempty"`
	AcceptedUnobservableCapability int `json:"accepted_unobservable_capability,omitempty"`
}

// add tallies one matched entry's kind.
func (c *AcceptKindCounts) add(kind acceptKind) {
	switch kind {
	case acceptKindUnobservableRunner:
		c.AcceptedUnobservableRunner++
	case acceptKindUnobservableCapability:
		c.AcceptedUnobservableCapability++
	default:
		c.AcceptedEquivalent++
	}
}

// merge folds other's tallies into c, for the two passes goMutantsReceipt
// makes over one parsed accept-list (survivors, then accepted timeouts).
func (c *AcceptKindCounts) merge(other AcceptKindCounts) {
	c.AcceptedEquivalent += other.AcceptedEquivalent
	c.AcceptedUnobservableRunner += other.AcceptedUnobservableRunner
	c.AcceptedUnobservableCapability += other.AcceptedUnobservableCapability
}
