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
	disabledTestReason = "Test is disabled (it.skip / xit / t.Skip / @pytest.mark.skip / return error.SkipZigTest). " +
		"A skipped test reports green while proving nothing, so a disabled test is an oracle that can never fail. " +
		"Delete the test or fix it — do not skip it to get a passing run."
)

// sleepRe matches the real-time sleep calls that show up in test code across
// the supported languages. It runs against masked source, so a `sleep`
// mentioned in a string or comment is already blanked and cannot match.
//
//   - time.[Ss]leep    — Go time.Sleep AND Python time.sleep (sync)
//   - time.(After|NewTimer|Tick) — Go channel-based waits; just as real-time as
//     Sleep. Matched without requiring a leading `<-` so the bare constructor
//     `time.NewTimer(d)` is caught too. AfterFunc/NewTicker are deliberately not
//     matched: AfterFunc schedules a callback rather than blocking, and Tick (the
//     wait func) is the smell, not Ticker construction.
//   - asyncio.sleep    — Python async sleep
//   - Thread.sleep / thread::sleep — Java / Rust
//   - setTimeout       — JS/TS. This also covers the promisified-sleep idiom
//     `await new Promise(r => setTimeout(r, ms))`: the masked code still contains
//     a literal `setTimeout(`, so no separate `new Promise` regex is needed (and
//     adding one would falsely block legitimate non-timer promises).
//
// Zig's real-time sleeps need no new alternative: `std.time.sleep(` contains the
// substring `time.sleep(` (matched by `time\.[Ss]leep`) and `std.Thread.sleep(`
// contains `Thread.sleep(` (matched by `[Tt]hread\.sleep`), so both are already
// caught — see TestSmell_Zig.
//
// The check gates test files only (see oracleSmells / smellCheck), so matching
// these broadly cannot block ordinary source.
var sleepRe = regexp.MustCompile(`(?:time\.[Ss]leep|time\.(?:After|NewTimer|Tick)|asyncio\.sleep|[Tt]hread\.sleep|(?:std::)?thread::sleep|setTimeout)\s*\(`)

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

// zigExpectEqualRe matches Zig's `expectEqual(A, B)` self-compare form (and its
// expectEqualStrings/Slices/Deep siblings), reached as `std.testing.expectEqual`
// or `try expectEqual`. A and B are captured for textual comparison; each
// operand is `[^(),]` so it carries no paren or comma — a call operand simply
// does not match (`expectEqual(@as(i32,3), x)` never trips), exactly as the jest
// and node forms exclude calls. The trailing `\)` pins it to the two-argument
// shape, which is all `expectEqual` takes.
var zigExpectEqualRe = regexp.MustCompile(`\bexpectEqual(?:Strings|Slices|Deep)?\s*\(\s*([^(),]+?)\s*,\s*([^(),]+?)\s*\)`)

// zigSkipRe matches Zig's test-skip idiom `return error.SkipZigTest`. A test
// returns this error to skip itself, so it is the Zig disabled-test oracle. The
// `\breturn\s+` prefix keeps it to the returned form, so merely naming the error
// (`error.SkipZigTest == e`) does not trip.
var zigSkipRe = regexp.MustCompile(`\breturn\s+error\.SkipZigTest\b`)

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

// skipDotRe matches the jest/vitest/mocha `.skip` method form. The leading
// keyword set bounds it to test constructs, so `unit.skip(` (no boundary before
// `it`) and unrelated `.skip(` chains do not match.
var skipDotRe = regexp.MustCompile(`\b(?:it|test|describe|context|suite)\s*\.\s*skip\s*\(`)

// skipXRe matches the x-prefixed disabled aliases (xit / xdescribe / …). Like
// focusedFnRe it needs the member-access guard so `obj.xit(` is rejected.
var skipXRe = regexp.MustCompile(`\b(?:xit|xdescribe|xcontext|xtest)\s*\(`)

// skipGoRe matches Go's t.Skip / t.Skipf / t.SkipNow. The receiver is held to a
// short lowercase handle (t, tt, b, tb) so a domain method like `reader.Skip(`
// is not misread as a test skip — missing an oddly-named handle is a tolerable
// false negative; a false edit-time block is not.
var skipGoRe = regexp.MustCompile(`\b[a-z][a-z0-9]?\.Skip(?:Now|f)?\s*\(`)

// skipPyRe matches the pytest / unittest skip decorators (skip and skipif).
var skipPyRe = regexp.MustCompile(`@\s*(?:pytest\s*\.\s*mark\s*\.\s*skip(?:if)?|unittest\s*\.\s*skip)`)

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
	disabledTestPolicy = policy{
		name: "disabled-test", category: smellCat, reason: disabledTestReason,
		hit: func(v view) bool { return hasDisabledTest(v.code) },
	}
)

// oracleSmells are the test-oracle integrity policies. They gate test files
// only — a sleep or a focused marker has no meaning in source — and block at
// every phase because each one is near-zero-false-positive.
var oracleSmells = []policy{sleepPolicy, tautologyPolicy, focusedPolicy, disabledTestPolicy}

// smellCheck runs the test-oracle smell policies at edit phase against new test
// content. It is a thin wrapper over the engine; the broader policy sets (which
// add suppressions, and gate source files too) compose the same policies.
func smellCheck(content string) Decision {
	return evaluate(content, oracleSmells, editPhase, defaultLang)
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
	for _, m := range zigExpectEqualRe.FindAllStringSubmatch(masked, -1) {
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

// hasDisabledTest reports whether masked source disables a test in any
// recognised form, rejecting `xit(`/`xdescribe(` that are really method calls.
func hasDisabledTest(masked string) bool {
	if skipDotRe.MatchString(masked) || skipGoRe.MatchString(masked) || skipPyRe.MatchString(masked) || zigSkipRe.MatchString(masked) {
		return true
	}
	for _, loc := range skipXRe.FindAllStringIndex(masked, -1) {
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
