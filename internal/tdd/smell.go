package tdd

import (
	"regexp"
	"strings"
)

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
	assertionFreeReason = "Test declares no assertion for its language anywhere in its body " +
		"(Go: t.Error/t.Fatal/t.Fail/require./assert./rapid./cmp.Diff; Rust: assert; Python: assert / pytest.raises; " +
		"JS: expect(). An oracle with no assertion passes no matter what the implementation does. " +
		"Add an assertion, or mark a deliberate no-assertion test with `// smoke-ok: <why>` trailing " +
		"on the test's own declaration line (or on the line directly above it)."
	errorKindBlindReason = "Test asserts only that an error occurred (require.Error(/assert.Error(/.is_err()/" +
		"pytest.raises(Exception)/toThrow() with no argument), never WHICH one — any failure, including the wrong " +
		"one, passes. Assert the specific error (ErrorIs/ErrorAs/ErrorContains/EqualError, matches!/match=, or " +
		"toThrow(SpecificError)), or justify checking only occurrence with `// any-error-ok: <why>`."
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

// assertTrueLiteralRe matches a single-OPERAND tautological assertion —
// hardcoding the boolean literal instead of comparing two values — alongside
// the two-operand self-comparisons above: Go/testify `assert.True(t, true)` /
// `require.True(t, true)`, Rust `assert!(true)`, and Python's bare
// `assert True`. Each can never fail no matter what the implementation does;
// there is simply only one operand to name. `expect(true).toBe(true)` needs no
// entry here — expectSelfRe already treats it as a two-operand self-compare
// since both captured operands are the literal text "true".
var assertTrueLiteralRe = regexp.MustCompile(`(?m)(?:assert|require)\.True\s*\(\s*[\w.]+\s*,\s*true\s*\)|assert!\s*\(\s*true\s*\)|\bassert\s+True\s*(?:,|$)`)

// assertionFreeDeclRes flattens testDeclRes (precommit_diffscan.go's per-
// language test-declaration shapes — the same ones fail-first uses to find a
// staged test) into one slice, matched per line without knowing the file's
// own extension: cross-language collision is implausible for these fixed
// declaration keywords, the same reasoning the combined regexes above already
// rely on.
var assertionFreeDeclRes = flattenTestDeclRes()

func flattenTestDeclRes() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, res := range testDeclRes {
		out = append(out, res...)
	}
	return out
}

// exemptDeclRe matches declaration shapes assertionFreeDeclRes's Go pattern
// also recognises (it is one combined `(Test|Benchmark|Fuzz|Example)`
// alternation, reused rather than duplicated) but whose absence of an
// assertion call is not a smell: TestMain is the package-level `go test`
// entry point (it calls m.Run(), never asserts anything itself); a
// Benchmark measures throughput, never asserts; an Example's oracle is its
// `// Output:` comment, matched against stdout by `go test` itself, never a
// call in the body.
var exemptDeclRe = regexp.MustCompile(`^\s*func\s+(?:TestMain\s*\(|Benchmark\w*\s*\(|Example\w*\s*\()`)

// assertionTokenRe matches ANY language's assertion call, checked without
// regard to which language the test is in, exactly like sleepRe already spans
// five languages in one pattern. Go: t.Error/t.Fatal/t.Fail (no trailing word
// boundary, so the formatted Errorf/Fatalf variants — the overwhelmingly
// common shape in table-driven tests — still match), require., assert.,
// rapid., cmp.Diff. Rust: assert (covers assert!/assert_eq!/assert_ne!/
// debug_assert!, and doubles as Go's testify assert. prefix). Python: assert /
// pytest.raises. JS: expect(. Zig rides along on the same two tokens the
// oracle smells above already cover it with (assertionFreeDeclRes reuses
// testDeclRes[".zig"], so a Zig `test { }` block is judged here too):
// `std.testing.expect(` matches expect( already, and expectEqual (plus its
// Strings/Slices/Deep siblings, exactly as zigExpectEqualRe above enumerates)
// needs its own alternative since "Equal" sits between "expect" and "(".
var assertionTokenRe = regexp.MustCompile(`\bt\.(?:Error|Fatal|Fail)|\brequire\.|\bassert|\brapid\.|\bcmp\.Diff\b|pytest\.raises|expect\s*\(|expectEqual`)

// errorKindBlindRe matches an assertion that checks only THAT an error
// occurred, never WHICH one: Go testify's require.Error(/assert.Error(, Rust's
// `.is_err()` inside an assert!/assert_eq!/… call, Python's
// pytest.raises(Exception) (the base class matches anything), and JS's bare
// toThrow() (no expected error/message).
var errorKindBlindRe = regexp.MustCompile(`require\.Error\(|assert\.Error\(|assert\w*!\s*\([^\n]*?\.is_err\(\)|pytest\.raises\(\s*Exception\s*\)|toThrow\(\s*\)`)

// errorKindSafeRe is what immunizes a blind check: naming the SPECIFIC error
// (ErrorIs/ErrorAs/ErrorContains/EqualError, matches!, match=) or a
// toThrow(...) that DOES carry an argument. The toThrow alternative requires a
// non-close-paren first character so it can never self-satisfy on the bare
// toThrow() the policy is looking for.
var errorKindSafeRe = regexp.MustCompile(`ErrorIs|ErrorAs|ErrorContains|EqualError|matches!|match\s*=|toThrow\(\s*[^\s)]`)

// The test-oracle smells, as policies. Each runs against the code view (strings
// and comments blanked) because every one matches executable test code, never a
// directive in a comment.
var (
	sleepPolicy = policy{
		name: "test-sleep", category: smellCat, reason: sleepReason,
		hit:    func(v view) bool { return sleepRe.MatchString(v.code) },
		escape: escapeSleep,
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
		hit:    func(v view) bool { return hasDisabledTest(v.code) },
		escape: escapeSkip,
	}
)

// oracleSmells are the test-oracle integrity policies. They gate test files
// only — a sleep or a focused marker has no meaning in source — and block at
// every phase because each one is near-zero-false-positive.
var oracleSmells = []policy{sleepPolicy, tautologyPolicy, focusedPolicy, disabledTestPolicy}

// assertionFreePolicy is deliberately suppressionCat, NOT smellCat, despite
// living in the same family as the oracle smells above: it is NOT
// near-zero-false-positive. Measured against this repo's own 251 test files
// (issue #319) with a naive "block on any hit" reading, it fired on 3 of
// ~1385 test functions — and all three were legitimate: a concurrent-access
// test whose only oracle is the race detector / absence of a panic
// (buildlock_test.go), and two tests that delegate their actual assertion to
// a shared helper (denylog_test.go, state_test.go) — exactly the two
// false-positive shapes the issue itself named as a concern before wiring
// this as a block. So it warns at edit and only denies at commit, same as
// error-kind-blind below, where a human is about to vouch for the change and
// a real hit gets `// smoke-ok: <why>` instead of silently blocking the edit
// loop on legitimate code.
var assertionFreePolicy = policy{
	name: "assertion-free", category: suppressionCat, reason: assertionFreeReason,
	hit:    func(v view) bool { return hasAssertionFreeTest(v.code) },
	escape: escapeSmoke,
}

// errorKindBlindPolicy is test-oracle-scoped like the smells above (a blind
// error-occurred check has no meaning in source), but suppressionCat rather
// than smellCat: unlike a tautology or a focused marker, checking only that
// SOME error occurred has a real use (a boundary test that genuinely does not
// care which failure mode fires), so it warns at edit and only blocks at
// commit — where the diff-scoped check (precommit_diffscan.go's
// newSuppression) judges solely the lines a commit ADDS, so a blind check that
// already lived in the tree never blocks a later, unrelated commit; only one
// this change introduces does.
var errorKindBlindPolicy = policy{
	name: "error-kind-blind", category: suppressionCat, reason: errorKindBlindReason,
	hit:    func(v view) bool { return hasErrorKindBlind(v.code) },
	escape: escapeAnyError,
}

// testOracleWarnings are suppressionCat policies scoped to test files only
// (like oracleSmells, not like suppressionPolicies' any-code-file scope) —
// composed into testPolicies for the edit-time gate and into the commit-time
// gate's test-file branch (precommit_diffscan.go's commitSuppressionPolicies).
var testOracleWarnings = []policy{assertionFreePolicy, errorKindBlindPolicy}

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
	return assertTrueLiteralRe.MatchString(masked)
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

// hasAssertionFreeTest reports whether masked source declares a test function
// whose body — from its declaration line through the line where indentation
// returns to the declaration's own level — carries no assertion token at all:
// an oracle that cannot fail no matter what the implementation does.
func hasAssertionFreeTest(masked string) bool {
	lines := strings.Split(masked, "\n")
	for i, line := range lines {
		if !isTestDeclLine(line) {
			continue
		}
		if !assertionTokenRe.MatchString(testFunctionBody(lines, i)) {
			return true
		}
	}
	return false
}

// isTestDeclLine reports whether line is shaped like a test declaration this
// policy judges — one of assertionFreeDeclRes's shapes, minus exemptDeclRe's
// TestMain/Benchmark/Example carve-out.
func isTestDeclLine(line string) bool {
	if exemptDeclRe.MatchString(line) {
		return false
	}
	for _, re := range assertionFreeDeclRes {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// topLevelStartRe is what a line must begin with (after masking, so a blanked
// string/comment byte never counts) to plausibly be the STATEMENT that ends a
// test function's body: a letter, underscore, `}`, or `@` (a Python
// decorator). Punctuation alone — most commonly the closing backtick of a Go
// raw-string literal embedding TOML/YAML/JSON verbatim at column zero, a bare
// backtick-close-paren on its own line — is excluded, because it is a stray
// delimiter fragment, never a real dedent back to a new top-level construct.
// Without this, that fragment's zero leading whitespace reads as "the
// function ended", truncating the body before it ever reaches the test's
// real assertion — the single largest false-positive source measured
// against this repo's own suite (issue #319).
var topLevelStartRe = regexp.MustCompile(`^[A-Za-z_}@]`)

// testFunctionBody returns lines[i:j] joined: from declaration line i through
// the line before indentation returns to (or below) the declaration's own
// AND plausibly starts a new top-level construct (see topLevelStartRe) — the
// closing brace for a brace language (Go/Rust/JS/Zig), or the next top-level
// statement for Python's indentation-only blocks. Ending is gated on having
// first seen a line MORE indented than the declaration (entered): Rust's
// `#[test]` attribute and the `fn` line it decorates share the SAME
// indentation, so without this gate the `fn` line itself would misread as an
// immediate dedent and truncate the body to the bare attribute, before ever
// reaching the real one. This is also why the scan does NOT stop at the next
// test-shaped line unconditionally: a `t.Run(` subtest inside a table-driven
// test's loop matches assertionFreeDeclRes too, but it is NESTED, not a new
// top-level test — ending there would truncate the outer test right before
// the loop that carries its actual assertion, the single most common Go test
// shape in this repo. No ending line found at all (the function runs to the
// end of what this edit shows) returns everything from i to EOF. Blank lines
// never end the body — an intervening blank inside a table-driven test is not
// a dedent.
func testFunctionBody(lines []string, i int) string {
	indent := leadingWidth(lines[i])
	entered := false
	j := i + 1
	for ; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if leadingWidth(lines[j]) > indent {
			entered = true
			continue
		}
		if entered && topLevelStartRe.MatchString(trimmed) {
			break
		}
	}
	return strings.Join(lines[i:j], "\n")
}

// leadingWidth counts the leading spaces/tabs on a line.
func leadingWidth(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}

// hasErrorKindBlind reports whether masked source contains a blind
// error-occurred-only check with no error-KIND safety net within two lines
// either side.
func hasErrorKindBlind(masked string) bool {
	lines := strings.Split(masked, "\n")
	for i, line := range lines {
		if !errorKindBlindRe.MatchString(line) {
			continue
		}
		if !errorKindSafeNearby(lines, i) {
			return true
		}
	}
	return false
}

// errorKindSafeNearby reports whether a safety-net token appears within two
// lines either side of i (inclusive of i itself, so a same-line
// `assert.ErrorIs(...)` written on the one line still counts).
func errorKindSafeNearby(lines []string, i int) bool {
	lo, hi := max(0, i-2), min(len(lines)-1, i+2)
	return errorKindSafeRe.MatchString(strings.Join(lines[lo:hi+1], "\n"))
}
