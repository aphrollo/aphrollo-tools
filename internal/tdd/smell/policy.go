package smell

import (
	"path/filepath"
	"strings"

	masklex "github.com/aphrollo/aphrollo-tools/internal/mask"
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
//     (reason: this block names the exact vocabulary the directives view exists to preserve.)
//   - whole: the code view of the ENTIRE file, kept alongside a restricted
//     one. A diff-scoped caller hands a policy only the lines an edit adds, so
//     a policy whose verdict depends on the structure around a line — is this
//     `case <-time.After(d)` the second arm of a select, or the only one? —
//     has no way to see it. Judging stays scoped to the added lines; only the
//     context is file-wide.
type view struct {
	Code       string
	directives string
	whole      string
}

func newView(content string, l lang) view {
	code := l.mask(content, true)
	return view{
		Code:       code,
		directives: l.mask(content, false),
		whole:      code,
	}
}

// mask blanks content's strings, and its comments when blankComments is set,
// lexing it as the file's language.
func (l lang) mask(content string, blankComments bool) string {
	if l.rust {
		return masklex.RustTokens(content, true, blankComments)
	}
	return maskTokens(content, true, blankComments, l.hashComment)
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

// lang captures the lexical quirks the masker must know about the edited file:
// whether `#` begins a line comment, and whether the file is Rust, where `'`
// is a char literal only in a char literal's shape and otherwise the sigil of
// a lifetime or a label — read as a quote, it blanks every line up to the next
// apostrophe and hides them from every detector. Getting `#` wrong matters
// for the suppression detectors, which read the comment-preserving view — a
// `#` wrongly treated as a comment stops the lexer skipping/scanning the rest
// of the line, so a directive-looking string after a JS private field could
// leak and trip a false suppression.
type lang struct {
	hashComment bool
	rust        bool
}

// langOf derives the lexical quirks from a file path. `#` is a comment in
// Python and Ruby; in Go it never appears and in JS/TS it is a private-field
// sigil, so the default is `#`-is-code.
func langOf(path string) lang {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".rb":
		return lang{hashComment: true}
	case ".rs":
		return lang{rust: true}
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
	// escape is the comment marker that admits a hit on the line carrying it
	// (or the line below), for the kinds with legitimate uses. Empty means the
	// policy admits none — a tautology has no good reason.
	escape string
}

// The composed gate sets, by what a phase is looking at. oracleSmells lives in
// smell.go and suppressionPolicies in suppress.go; here they are combined into
// the sets the edit-time gate selects between by file kind.
var (
	// testPolicies gate a test-file edit: oracle smells, the test-scoped
	// suppressionCat warnings (testOracleWarnings), AND the any-code-file
	// suppressions.
	testPolicies = concatPolicies(oracleSmells, testOracleWarnings, suppressionPolicies)
	// sourcePolicies gate a source-file edit: suppressions only, since the
	// oracle smells and the test-scoped warnings have no meaning outside test
	// code.
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

// categoryActions is the (category, phase) → Action matrix, built once. Smells
// always block; suppressions warn at edit and block at commit. actionFor is a
// lookup into it, so the reaction table is data, not control flow.
var categoryActions = map[category]map[phase]Action{
	smellCat:       {editPhase: Block, commitPhase: Block},
	suppressionCat: {editPhase: Warn, commitPhase: Block},
}

// actionFor maps a tripped policy's category to the action a given phase takes.
func actionFor(c category, p phase) Action {
	return categoryActions[c][p]
}

// evaluate runs policies against content for a phase and returns the most severe
// Decision (Block > Warn > Allow); ties go to the first policy in slice order.
// Taking the most severe — rather than the first hit — means a Block smell
// always wins over a Warn suppression in the same edit regardless of ordering,
// so the policy slices can be composed without ordering fragility.
func evaluate(content string, policies []policy, p phase, l lang) Decision {
	return evaluateView(newView(content, l), policies, p)
}

// evaluateView is evaluate over a pre-built view. A diff-scoped caller wants
// evaluateAdded instead (below) — it masks a file's FULL post-image (balanced
// quote/comment context) before restricting to added lines, instead of
// masking the deletion-stripped added-only buffer where an unbalanced quote on
// one added line would blank a later added line's directive to EOF and
// smuggle it past the gate — and it honours each policy's escape marker,
// which evaluateView does not.
func evaluateView(v view, policies []policy, p phase) Decision {
	best := Decision{Action: Allow}
	for _, pol := range policies {
		if !pol.hit(v) {
			continue
		}
		if a := actionFor(pol.category, p); a > best.Action {
			best = Decision{Action: a, Reason: pol.reason, Policy: pol.name}
			if best.Action == Block {
				break // nothing outranks Block; stop early
			}
		}
	}
	return best
}
