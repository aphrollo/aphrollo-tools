package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// noEscapeAllowlist names every policy that DELIBERATELY carries no escape
// token, and why. policy.escape is empty by default, so a policy that simply
// never had one wired up looks identical to one that made the choice on
// purpose — the same shape #320's stand-down census exists for, one level
// down: an absent escape is invisible unless something names it. A policy
// not on this list must carry a non-empty escape (issue #321); adding a name
// here without a real reason is exactly the gap this test exists to catch.
var noEscapeAllowlist = map[string]string{
	// smellCat: no legitimate use exists, ever — the reason text itself says
	// "remove it", not "justify it".
	"tautology":    "a self-comparison assertion can never fail no matter what the code does; see tautologyReason",
	"focused-test": "a focused marker silently drops every other test from the run; see focusedReason",
	// suppressionCat: the escape mechanism is category-level, not a per-policy
	// comment token — these WARN at edit time and BLOCK at commit, where a
	// human is about to vouch for the change (see the doc comment on
	// suppressionCat and on suppress.go's own package comment).
	"lint-suppress":     "suppressionCat warns at edit and blocks at commit; admitted by review, not a comment marker",
	"type-suppress":     "suppressionCat warns at edit and blocks at commit; admitted by review, not a comment marker",
	"coverage-suppress": "suppressionCat warns at edit and blocks at commit; admitted by review, not a comment marker",
}

// TestPolicyRegistry_EveryPolicyHasAnEscapeOrIsOnTheNoEscapeAllowlist mirrors
// what the ratchet engine already requires of a law's escape: absence is a
// stated choice, never a default nobody noticed.
func TestPolicyRegistry_EveryPolicyHasAnEscapeOrIsOnTheNoEscapeAllowlist(t *testing.T) {
	for _, p := range testPolicies {
		if p.escape != "" {
			continue
		}
		if reason, ok := noEscapeAllowlist[p.name]; !ok || reason == "" {
			t.Errorf("policy %q has no escape token and is not on noEscapeAllowlist with a reason "+
				"— a missing escape must be a stated choice, not a gap", p.name)
		}
	}
	for name := range noEscapeAllowlist {
		if !policyRegistered(name) {
			t.Errorf("noEscapeAllowlist names %q, which is not a registered policy — a stale entry hides nothing anymore", name)
		}
	}
}

// policyRegistered reports whether name is one of testPolicies' own names.
func policyRegistered(name string) bool {
	for _, p := range testPolicies {
		if p.name == name {
			return true
		}
	}
	return false
}

// TestPolicyRegistry_EveryPolicyHasAHitAndCleanFixture proves each policy's
// predicate against a real file on disk, the way a ratchet law's own
// fixtures prove it — hit-only proves a policy fires, never that it
// discriminates; both directions are required.
func TestPolicyRegistry_EveryPolicyHasAHitAndCleanFixture(t *testing.T) {
	for _, p := range testPolicies {
		dir := filepath.Join("testdata", "policies", p.name)
		hit, err := os.ReadFile(filepath.Join(dir, "hit.txt"))
		if err != nil {
			t.Errorf("%s: no hit fixture — a policy nobody proved catches nothing (%v)", p.name, err)
			continue
		}
		clean, err := os.ReadFile(filepath.Join(dir, "clean.txt"))
		if err != nil {
			t.Errorf("%s: no clean fixture — a hit-only policy is never proved to discriminate (%v)", p.name, err)
			continue
		}
		if !p.hit(newView(string(hit), defaultLang)) {
			t.Errorf("%s: hit.txt did not trip the policy", p.name)
		}
		if p.hit(newView(string(clean), defaultLang)) {
			t.Errorf("%s: clean.txt tripped the policy", p.name)
		}
	}
}
