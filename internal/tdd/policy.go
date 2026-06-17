package tdd

import (
	"path/filepath"
	"strings"
)

// A gate is a list of policies evaluated against the new content of one edit.
// This file is the engine; the individual detectors live in smell.go (test
// oracle integrity) and suppress.go (silenced quality gates). Splitting the
// engine from the detectors is what lets a new gate be a data entry in a slice
// rather than a new branch of control flow.

// category groups detectors by the kind of integrity they protect, which is
// what decides how hard each phase reacts to a hit.
type category int

const (
	// smellCat: the test ORACLE itself is untrustworthy — a real-time sleep, a
	// tautological assertion, a focused or skipped test. These are
	// near-zero-false-positive (they have no legitimate use in a test that is
	// meant to pin behavior), so they BLOCK at every phase, edit time included.
	smellCat category = iota
	// suppressionCat: a quality GATE is being silenced — the linter, the type
	// checker, coverage. Legitimate, reviewed uses exist, so per the design
	// rule that higher-false-positive checks must not wedge an edit, these only
	// WARN at edit time and BLOCK at commit, where a human is about to vouch for
	// the change.
	suppressionCat
)

// view holds the edit content prepared two ways, computed once per evaluation
// so every policy shares the masking work:
//
//   - code: string literals AND comments blanked. Detectors that match
//     executable code (sleepRe, it.only, it.skip) run against this, so the same
//     token mentioned in prose never trips.
//   - directives: string literals blanked, comments PRESERVED. Suppression
//     directives (//nolint, // @ts-ignore, # type: ignore) live in comments, so
//     they must stay visible; strings are blanked so a quoted directive cannot
//     trip.
type view struct {
	code       string
	directives string
}

func newView(content string, l lang) view {
	return view{
		code:       maskTokens(content, true, true, l.hashComment),
		directives: maskTokens(content, true, false, l.hashComment),
	}
}

// addedView masks the FULL post-image of a file (so the lexer sees balanced
// string/comment context across every line) and then keeps only the lines this
// change added. Detectors thus judge solely the introduced lines while the
// masking can never be fooled by an opener whose partner sits on an unchanged
// line. added holds 1-based line numbers in postImage.
func addedView(postImage string, added map[int]bool, l lang) view {
	full := newView(postImage, l)
	return view{
		code:       keepLines(full.code, added),
		directives: keepLines(full.directives, added),
	}
}

// keepLines returns masked restricted to the 1-based line numbers in keep,
// preserving their content (already masked) and order.
func keepLines(masked string, keep map[int]bool) string {
	var b strings.Builder
	for i, line := range strings.Split(masked, "\n") {
		if keep[i+1] {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// lang captures the lexical quirks the masker must know about the edited file.
// Today that is exactly one: whether `#` begins a line comment. Getting it
// wrong matters for the suppression detectors, which read the comment-preserving
// view — a `#` wrongly treated as a comment stops the lexer skipping/scanning
// the rest of the line, so a directive-looking string after a JS private field
// could leak and trip a false suppression.
type lang struct {
	hashComment bool
}

// langOf derives the lexical quirks from a file path. `#` is a comment in
// Python and Ruby; in Go it never appears and in JS/TS it is a private-field
// sigil, so the default is `#`-is-code.
func langOf(path string) lang {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".rb":
		return lang{hashComment: true}
	default:
		return lang{hashComment: false}
	}
}

// defaultLang is for content evaluated without a path (test helpers). It assumes
// the C-family majority where `#` is not a comment.
var defaultLang = lang{hashComment: false}

// policy is one detector: a name (for tests and future telemetry), the
// integrity category it protects, the reason+fix to surface on a hit, and the
// predicate over a prepared view.
type policy struct {
	name     string
	category category
	reason   string
	hit      func(v view) bool
}

// The composed gate sets, by what a phase is looking at. oracleSmells lives in
// smell.go and suppressionPolicies in suppress.go; here they are combined into
// the sets the edit-time gate selects between by file kind.
var (
	// testPolicies gate a test-file edit: oracle smells AND suppressions.
	testPolicies = concatPolicies(oracleSmells, suppressionPolicies)
	// sourcePolicies gate a source-file edit: suppressions only, since the
	// oracle smells have no meaning outside test code.
	sourcePolicies = suppressionPolicies
)

// concatPolicies flattens policy sets into one slice (a fresh backing array, so
// no set aliases another).
func concatPolicies(sets ...[]policy) []policy {
	var out []policy
	for _, s := range sets {
		out = append(out, s...)
	}
	return out
}

// phase is when a gate is evaluating: edit time (PreToolUse, advisory bias) vs
// commit time (the authoritative wall).
type phase int

const (
	editPhase phase = iota
	commitPhase
)

// actionFor maps a tripped policy's category to the action a given phase takes.
// Smells always block; suppressions warn at edit and block at commit.
func actionFor(c category, p phase) Action {
	if c == smellCat {
		return Block
	}
	if p == commitPhase {
		return Block
	}
	return Warn
}

// evaluate runs policies against content for a phase and returns the most severe
// Decision (Block > Warn > Allow); ties go to the first policy in slice order.
// Taking the most severe — rather than the first hit — means a Block smell
// always wins over a Warn suppression in the same edit regardless of ordering,
// so the policy slices can be composed without ordering fragility.
func evaluate(content string, policies []policy, p phase, l lang) Decision {
	return evaluateView(newView(content, l), policies, p)
}

// evaluateView is evaluate over a pre-built view. The commit gate uses it so it
// can mask a file's FULL post-image (balanced quote/comment context) and then
// restrict the view to added lines, instead of masking the deletion-stripped
// added-only buffer — where an unbalanced quote on one added line would blank a
// later added line's directive to EOF and smuggle it past the gate.
func evaluateView(v view, policies []policy, p phase) Decision {
	best := Decision{Action: Allow}
	for _, pol := range policies {
		if !pol.hit(v) {
			continue
		}
		if a := actionFor(pol.category, p); a > best.Action {
			best = Decision{Action: a, Reason: pol.reason}
			if best.Action == Block {
				break // nothing outranks Block; stop early
			}
		}
	}
	return best
}
