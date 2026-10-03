package kernel

import "slices"

// LookupRule is the row of the rule table named id, for a reader that explains
// a past decision (`why`). The table stays read only: the row is a copy.
func LookupRule(id string) (Rule, bool) {
	i := slices.IndexFunc(ruleTable, func(r Rule) bool { return r.ID == id })
	if i < 0 {
		return Rule{}, false
	}
	return ruleTable[i], true
}

// DefaultLevel is the level the rule holds with no config: no pin and no key
// moved it (§5).
func (r Rule) DefaultLevel() Level {
	lv, _ := Config{}.level(r)
	return lv
}

// InHoldout says whether the lane is in the shadow arm of the rule (§5 "The
// holdout"): the same hash Decide reads, and only an earned block is shadowed.
func InHoldout(lane string, r Rule) bool {
	return r.Class == RuleEarned && heldOut(lane, r.ID)
}
