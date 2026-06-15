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
	Green             Outcome = "green"               // passed, source edit, clean output
	GreenWithWarnings Outcome = "green-with-warnings" // passed, source edit, warnings present
	WritingTest       Outcome = "writing-test"        // passed but NO tests actually ran (scaffolding)
	RedTautology      Outcome = "red-tautology"       // passed on a test edit — the test can't be failing first
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
var zeroTestsRe = regexp.MustCompile(`(?i)no tests? (?:found|to run|ran|executed)|no test files|collected 0 items|\b0 tests?\b|\btests?:\s+0\b|testing: warning: no tests to run`)

// warningRe marks otherwise-clean output as carrying warnings.
var warningRe = regexp.MustCompile(`(?i)\bwarning:|\bdeprecat|\bunused (?:variable|import)\b`)

// setupErrRe matches a broken test SETUP — syntax, import, or collection
// errors — which means "fix the test, not the implementation". It is
// intentionally narrow: an ambiguous failure falls through to a plain Red
// rather than being mislabeled (the audit's Go-testdata / Python-import FPs).
var setupErrRe = regexp.MustCompile(`(?i)syntaxerror|indentationerror|importerror|modulenotfounderror|error collecting|cannot find module|transform failed|\berror ts\d+\b`)

// missingImplRe matches the canonical clean-RED signal: the symbol under test
// does not exist yet. This is the expected first step of a TDD cycle.
var missingImplRe = regexp.MustCompile(`(?i)undefined: |is not defined|has no attribute|cannot find name|no such|undeclared name`)

// ClassifyOutcome maps a test run to an Outcome. prevFailing is the failing-test
// set recorded after the previous edit, used to recognise that a still-failing
// run introduced NO new failures (no-delta) so the agent is not nagged about
// pre-existing breakage.
func ClassifyOutcome(passed bool, output string, kind Kind, prevFailing []string) Outcome {
	if passed {
		switch {
		case zeroTestsRe.MatchString(output):
			return WritingTest
		case kind == Test:
			// A freshly written/edited test that passes immediately never
			// went RED — it cannot be pinning the behavior it claims to.
			return RedTautology
		case warningRe.MatchString(output):
			return GreenWithWarnings
		default:
			return Green
		}
	}

	switch {
	case setupErrRe.MatchString(output):
		return RedBogus
	case missingImplRe.MatchString(output):
		return RedMissingImpl
	}

	if len(prevFailing) > 0 && noNewFailures(ExtractFailingTests(output), prevFailing) {
		return NoDelta
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
	regexp.MustCompile(`(?m)^\s*--- FAIL:\s+(\S+)`),                    // go test
	regexp.MustCompile(`(?m)^FAILED\s+(\S+::\S+)`),                     // pytest: FAILED path::test
	regexp.MustCompile(`(?m)^(\S+::\S+)\s+FAILED`),                     // pytest: path::test FAILED
	regexp.MustCompile(`(?m)^\s*[✗×]\s+(.+?)(?:\s+\(\d+\s*m?s\))?\s*$`), // vitest/jest
	regexp.MustCompile(`(?m)^test\s+(\S+)\s+\.\.\.\s+FAILED`),          // cargo
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
