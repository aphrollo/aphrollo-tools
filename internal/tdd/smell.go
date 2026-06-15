package tdd

import "regexp"

// Smell reasons name the problem AND the fix — a blocked edit must tell the
// agent how to proceed, never just "no".
const (
	sleepReason = "Test contains a real-time sleep (time.Sleep / setTimeout-as-wait / asyncio.sleep). " +
		"Real-time sleeps make tests slow and flaky. Use a fake clock, an injected timer, or explicit synchronization instead."
	tautologyReason = "Test contains a tautological assertion — both operands are the same expression (self-comparison), " +
		"so the oracle can never fail no matter what the implementation does. Assert the real value/state against an independent expected value."
	focusedReason = "Test contains a focused marker (it.only / describe.only / fit / fdescribe). " +
		"A focused test silently drops every other test from the run — the suite reports green while most of it never ran. Remove the focus."
)

// sleepRe matches the real-time sleep calls that show up in test code across
// the supported languages. It runs against masked source, so a `sleep`
// mentioned in a string or comment is already blanked and cannot match.
var sleepRe = regexp.MustCompile(`(?:time\.[Ss]leep|asyncio\.sleep|[Tt]hread\.sleep|(?:std::)?thread::sleep|setTimeout)\s*\(`)

// selfCompareRe matches an equality whose two operands are simple expressions
// (identifier, member access, index, or literal — note the char class has no
// `(`, so function calls are structurally excluded). Excluding calls is the
// key false-positive fix: `expect(fn()).toBe(fn())` is legitimate because the
// two calls can return different values, whereas `assert x == x` cannot.
var selfCompareRe = regexp.MustCompile(`([\w.\[\]]+)\s*===?\s*([\w.\[\]]+)`)

// expectSelfRe matches a jest-style `expect(A).matcher(B)` where A and B are
// captured for textual comparison. `[^()]*?` keeps each operand free of
// parens, so a call operand simply does not match (no false block).
var expectSelfRe = regexp.MustCompile(`expect\s*\(\s*([^()]*?)\s*\)\s*\.\s*(?:toBe|toEqual|toStrictEqual|toMatchObject)\s*\(\s*([^()]*?)\s*\)`)

// nodeEqualRe matches Node's assert.equal / strictEqual / deepEqual family,
// capturing the first two arguments (actual, expected). The testify three-arg
// form (Equal(t, a, b)) is intentionally not matched: missing it is a false
// NEGATIVE (a smell slips to the push-time review), never a false block.
var nodeEqualRe = regexp.MustCompile(`\bassert\s*\.\s*(?:strictEqual|deepStrictEqual|deepEqual|equal)\s*\(\s*([^(),]+?)\s*,\s*([^(),]+?)\s*[,)]`)

// focusedOnlyRe matches `it.only(` / `describe.only(` etc. The trailing `(`
// requires a call, so a property assignment like `context.only = 5` does not
// match.
var focusedOnlyRe = regexp.MustCompile(`\b(?:it|test|describe|context|suite)\s*\.\s*only\s*\(`)

// focusedFnRe matches the `fit(` / `fdescribe(` / `fcontext(` aliases. A word
// boundary alone is not enough — `model.fit(` would match — so checkFocused
// additionally rejects a match preceded by `.`.
var focusedFnRe = regexp.MustCompile(`\b(?:fit|fdescribe|fcontext)\s*\(`)

// The test-oracle smells, as policies. Each runs against the code view (strings
// and comments blanked) because every one matches executable test code, never a
// directive in a comment.
var (
	sleepPolicy = policy{
		name: "test-sleep", category: smellCat, reason: sleepReason,
		hit: func(v view) bool { return sleepRe.MatchString(v.code) },
	}
	tautologyPolicy = policy{
		name: "tautology", category: smellCat, reason: tautologyReason,
		hit: func(v view) bool { return hasTautology(v.code) },
	}
	focusedPolicy = policy{
		name: "focused-test", category: smellCat, reason: focusedReason,
		hit: func(v view) bool { return hasFocused(v.code) },
	}
)

// smellCheck runs the test-oracle smell policies at edit phase against new test
// content. It is the thin wrapper the test-file edit path uses; the broader
// policy sets (which add suppressions, and gate source files too) compose the
// same policies through evaluate.
func smellCheck(content string) Decision {
	return evaluate(content, []policy{sleepPolicy, tautologyPolicy, focusedPolicy}, editPhase)
}

// hasTautology reports whether masked source contains a self-comparison
// assertion in any of the recognised forms.
func hasTautology(masked string) bool {
	for _, m := range selfCompareRe.FindAllStringSubmatch(masked, -1) {
		if m[1] == m[2] {
			return true
		}
	}
	for _, m := range expectSelfRe.FindAllStringSubmatch(masked, -1) {
		if m[1] != "" && m[1] == m[2] {
			return true
		}
	}
	for _, m := range nodeEqualRe.FindAllStringSubmatch(masked, -1) {
		if m[1] == m[2] {
			return true
		}
	}
	return false
}

// hasFocused reports whether masked source contains a focused-test marker,
// rejecting `fit(`/`fdescribe(` that are really method calls (`obj.fit(`).
func hasFocused(masked string) bool {
	if focusedOnlyRe.MatchString(masked) {
		return true
	}
	for _, loc := range focusedFnRe.FindAllStringIndex(masked, -1) {
		if i := loc[0]; i == 0 || !isMemberAccess(masked[i-1]) {
			return true
		}
	}
	return false
}

// isMemberAccess reports whether c makes the following token a member access or
// part of a longer identifier — i.e. `.` or a word character — in which case a
// `fit(` match is `something.fit(`/`prefit(`, not the focused-test alias.
func isMemberAccess(c byte) bool {
	return c == '.' || c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
