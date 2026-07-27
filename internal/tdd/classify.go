package tdd

import (
	"regexp"
	"sort"
	"strings"
)

// Outcome is the classified result of a test run after an edit. It is the
// signal PostToolUse reports back to the model. The vocabulary is deliberately
// small: enough to tell "keep going" from "you broke something" from "your
// test can't fail", without the speculative sub-categories that made the
// original classifier brittle.
type Outcome string

const (
	Green             Outcome = "green"               // passed, clean output
	GreenWithWarnings Outcome = "green-with-warnings" // passed, warnings present
	WritingTest       Outcome = "writing-test"        // passed but NO tests actually ran (scaffolding)
	RedMissingImpl    Outcome = "red-missing-impl"    // failed: the symbol under test is undefined (clean RED)
	RedBogus          Outcome = "red-bogus"           // failed: test setup is broken (syntax/import/collection)
	Red               Outcome = "red"                 // failed: a plain assertion failure
	NoDelta           Outcome = "no-delta"            // failed, but only with pre-existing failures
)

// IsRed reports whether the outcome is actionable failure the agent should see.
// PostToolUse is silent unless the outcome IsRed, so green/writing-test/no-delta
// runs add no noise.
func (o Outcome) IsRed() bool {
	return strings.HasPrefix(string(o), "red")
}

// zeroTestsRe recognises a passing run in which no test actually executed —
// the single most important false-NEGATIVE fix. The original only caught
// "collected 0 items"/"no test files", so vitest's "0 tests" and a bare
// "0 passed" were stamped GREEN, hiding scaffolding that was never exercised.
// The `all 0 tests? passed` alternative is Zig's no-tests-ran summary:
// `zig test` prints `All 0 tests passed.` when a file/step executed none.
// `\b0 tests?\b` already covers the bare count, but the phrase is kept
// explicit so the Zig signal is legible.
var zeroTestsRe = regexp.MustCompile(`(?i)no tests? (?:found|to run|ran|executed)|no test files|collected 0 items|\b0 tests?\b|\btests?:\s+0\b|testing: warning: no tests to run|all 0 tests? passed`)

// warningRe marks otherwise-clean output as carrying warnings.
var warningRe = regexp.MustCompile(`(?i)\bwarning:|\bdeprecat|\bunused (?:variable|import)\b`)

// setupErrRe matches a broken test SETUP — syntax, import, or collection
// errors — which means "fix the test, not the implementation". It is
// intentionally narrow: an ambiguous failure falls through to a plain Red
// rather than being mislabeled (the audit's Go-testdata / Python-import FPs).
// The Zig alternatives catch a structural compile failure (`zig build test`
// emits `error: expected <token>` for a parse/type error, and a `referenced
// by:` trail under a propagated @compileError) — distinct from a clean
// missing-symbol RED, which missingImplRe catches below. setupErrRe is checked
// first, so it must NOT match Zig's undeclared-identifier / no-member output
// (both end in the generic `error: N compilation errors`, deliberately not
// keyed on here).
var setupErrRe = regexp.MustCompile(`(?i)syntaxerror|indentationerror|importerror|modulenotfounderror|error collecting|cannot find module|transform failed|\berror ts\d+\b|error: expected |referenced by:`)

// missingImplRe matches the canonical clean-RED signal: the symbol under test
// does not exist yet. This is the expected first step of a TDD cycle.
// The Zig alternatives are its undefined-symbol phrasings: `use of undeclared
// identifier` (a bare name with no decl), `use of undefined identifier` (older
// wording, kept for forward/back compat), and `has no member named` (a missing
// field/decl on a struct, e.g. the library root) — all the clean-RED "write
// the impl next" signal. The Rust alternatives are rustc's missing-symbol
// diagnostics: `cannot find function/value/…` (E0425/E0412), `no method named`
// (E0599), and `use of undeclared crate or module` (E0433). There is
// deliberately no bare `no such` alternative: a runtime "no such file or
// directory" in an assertion message is a plain failure, not a missing
// implementation.
var missingImplRe = regexp.MustCompile(`(?i)undefined: |is not defined|has no attribute|cannot find name|cannot find (?:function|value|struct|type|trait|macro|method)|no method named|undeclared name|use of undeclared (?:identifier|crate or module)|use of undefined identifier|has no member named`)

// ClassifyOutcome maps a test run to an Outcome. prevFailing is the failing-test
// set recorded after the previous edit, used to recognise that a still-failing
// run introduced NO new failures (no-delta) so the agent is not nagged about
// pre-existing breakage.
//
// A PASSING run is never RED here — not even a freshly edited test that passes
// immediately. Post-edit cannot know whether the implementation already existed
// (backfilling coverage, splitting a case, and refactoring a test all pass
// legitimately), so "passed test ⇒ tautology" would be a guess that fires
// constantly on a mature codebase, breaking the silent-on-green contract. The
// authoritative fail-first check is precommit, which runs the new tests in a
// worktree WITHOUT the new source and can actually tell.
func ClassifyOutcome(passed bool, output string, prevFailing []string) Outcome {
	if passed {
		switch {
		case zeroTestsRe.MatchString(output):
			return WritingTest
		case warningRe.MatchString(output):
			return GreenWithWarnings
		default:
			return Green
		}
	}

	// The failing-set delta is judged BEFORE the regex classes: a run whose
	// failures were all already failing is NoDelta no matter what its message
	// text matches. A pre-existing failure often carries missing-impl/setup-error
	// phrasing ("has no attribute", "ImportError: …"), and relabeling it
	// red-missing-impl on every unrelated edit nags the agent about breakage it
	// did not cause. A run with no parseable failing names (e.g. a compile error)
	// never qualifies as NoDelta, so fresh clean-RED signals keep their class.
	if len(prevFailing) > 0 && noNewFailures(ExtractFailingTests(output), prevFailing) {
		return NoDelta
	}

	switch {
	case setupErrRe.MatchString(output):
		return RedBogus
	case missingImplRe.MatchString(output):
		return RedMissingImpl
	}
	return Red
}

// noNewFailures reports whether every currently-failing test was already
// failing before this edit (curr ⊆ prev).
func noNewFailures(curr, prev []string) bool {
	if len(curr) == 0 {
		return false // a failed run with no parsed names is not provably pre-existing
	}
	prevSet := make(map[string]bool, len(prev))
	for _, p := range prev {
		prevSet[p] = true
	}
	for _, c := range curr {
		if !prevSet[c] {
			return false
		}
	}
	return true
}

// failLineRes extract failing test names across the supported runners. Each is
// anchored to a per-line failure marker; the vitest/jest form strips a trailing
// `(123 ms)` duration WITHOUT truncating a name that itself contains
// parentheses (the audit's non-greedy-capture fix).
var failLineRes = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^\s*--- FAIL:\s+(\S+)`),                     // go test
	regexp.MustCompile(`(?m)^FAILED\s+(\S+::\S+)`),                      // pytest: FAILED path::test
	regexp.MustCompile(`(?m)^(\S+::\S+)\s+FAILED`),                      // pytest: path::test FAILED
	regexp.MustCompile(`(?m)^\s*[✗×]\s+(.+?)(?:\s+\(\d+\s*m?s\))?\s*$`), // vitest/jest
	regexp.MustCompile(`(?m)^test\s+(\S+)\s+\.\.\.\s+FAILED`),           // cargo
	regexp.MustCompile(`(?m)^\s*error: '([^']+)' failed:`),             // zig build test
}

// ExtractFailingTests returns the sorted, de-duplicated set of failing test
// names found in runner output. Sorting makes the set stable for delta
// comparison across runs.
func ExtractFailingTests(output string) []string {
	seen := map[string]bool{}
	for _, re := range failLineRes {
		for _, m := range re.FindAllStringSubmatch(output, -1) {
			if name := strings.TrimSpace(m[1]); name != "" {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
