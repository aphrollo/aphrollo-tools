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
	errorKindBlindReason = "Test asserts only that an error occurred (require.Error(/assert.Error(/.is_err()/" +
		"pytest.raises(Exception)/toThrow() with no argument), never WHICH one — any failure, including the wrong " +
		"one, passes. Assert the specific error (ErrorIs/ErrorAs/ErrorContains/EqualError, matches!/match=, or " +
		"toThrow(SpecificError)), or justify checking only occurrence with `// any-error-ok: <why>`."
	panicOnlyOracleReason = "Test's only assertion is a defer/recover panic-catcher, and a call under test has its " +
		"result discarded (`_ = fn(...)`) rather than checked — this proves the code does not panic and nothing " +
		"whatsoever about what it computes. If not panicking really is the whole claim (a void-returning smoke " +
		"check has nothing else to assert), mark it `// panic-only-ok: <why>`; otherwise assert on the discarded value."
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
//     wait func) is the smell, not Ticker construction. One time.After shape is
//     exempted after the fact — a `case <-time.After(d):` arm of a select that
//     has another arm, which bounds a wait rather than being one. That is
//     hasRealTimeWait's job, not this regex's; see smell_deadline.go for the
//     measurement (#698) and for why the exemption is this narrow.
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

// goDeferRecoverRe matches the opening of a deferred anonymous func literal —
// `defer func() {` — the exact shape every recover-guarded panic check in
// this repo's fuzz/property tests uses (issue #319's incident 8).
var goDeferRecoverRe = regexp.MustCompile(`^\s*defer\s+func\s*\(\s*\)\s*\{`)

// recoverCallRe matches the recover() call itself, distinguishing a genuine
// panic-catching defer from an unrelated deferred cleanup (t.Cleanup-style)
// that happens to also assert — only a defer that actually calls recover()
// is "the panic handler" for panicOnlyOracleBody's purposes.
var recoverCallRe = regexp.MustCompile(`\brecover\s*\(\s*\)`)

// goAssertionTokenRe matches a Go assertion call: t.Error/t.Fatal/t.Fail (no
// trailing word boundary, so the formatted Errorf/Fatalf/Failf siblings still
// match), testify's require./assert., rapid's rapid., and go-cmp's cmp.Diff.
var goAssertionTokenRe = regexp.MustCompile(`\bt\.(?:Error|Fatal|Fail)|\brequire\.|\bassert\.|\brapid\.|\bcmp\.Diff\b`)

// goDiscardCallRe matches a blank-identifier discard of a function call's
// return value(s) — `_ = f(...)`, `_, x := f(...)`, `_, _ = f(...)` — the
// shape a fuzz/property target that computes something and checks nothing
// about it uses. The discard is load-bearing: it is what turns "asserts only
// that the code does not panic" from a legitimate void-returning smoke check
// into a finding — a fuzz target whose function under test truly returns
// nothing has no discard to match, and never trips this policy.
var goDiscardCallRe = regexp.MustCompile(`\b_\s*(?:,\s*[\w.]+\s*)*:?=\s*[A-Za-z_][\w.]*\s*\(`)

// The test-oracle smells, as policies. Each runs against the code view (strings
// and comments blanked) because every one matches executable test code, never a
// directive in a comment.
var (
	sleepPolicy = policy{
		name: "test-sleep", category: smellCat, reason: sleepReason,
		hit:    hasRealTimeWait,
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

// panicOnlyOraclePolicy names issue #319's own incident 8: six fuzz tests
// fixed in one commit (76f9c48) shared this exact shape — `defer func(){ if
// r := recover(); r != nil { t.Fatalf(...) } }()` as the ONLY assertion, with
// the function under test's return value discarded (`_ = fn(...)`)
// elsewhere in the body. Unlike assertion-free (dropped — see git history
// and #319: 0 of 8 recorded incidents caught, 3 measured hits all false
// positives), this shape is the one the issue's own evidence actually
// contains: 4 of the 6 named fuzz tests reconstruct exactly this way at
// 76f9c48~1 (FuzzCargoShimArgv, FuzzGitShimArgv, FuzzBashWriteTargets and a
// fourth since deleted with the stage it covered — the other two named,
// FuzzDocsOnlyClassifier and law_fuzz_test.go's FuzzParseLaw, turned out to
// be a different production bug and an already-partially-asserting test
// respectively, not this shape).
// suppressionCat: a fuzz/property target whose function under test
// genuinely returns nothing has no discard to trip this, so the legitimate
// "just don't panic" case is common and structural — warn at edit, deny at
// commit, escaped with `// panic-only-ok: <why>`.
var panicOnlyOraclePolicy = policy{
	name: "panic-only-oracle", category: suppressionCat, reason: panicOnlyOracleReason,
	hit:    func(v view) bool { return hasPanicOnlyOracle(v.code) },
	escape: escapePanicOnly,
}

// testOracleWarnings are suppressionCat policies scoped to test files only
// (like oracleSmells, not like suppressionPolicies' any-code-file scope) —
// composed into testPolicies for the edit-time gate and into the commit-time
// gate's test-file branch (precommit_diffscan.go's commitSuppressionPolicies).
var testOracleWarnings = []policy{errorKindBlindPolicy, panicOnlyOraclePolicy}

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

// topLevelStartRe is what a line must begin with (after masking, so a blanked
// string/comment byte never counts) to plausibly be the STATEMENT that ends a
// test function's body: a letter, underscore, `}`, or `@` (a Python
// decorator). Punctuation alone — most commonly the closing backtick of a Go
// raw-string literal embedding TOML/YAML/JSON verbatim at column zero, a bare
// backtick-close-paren on its own line — is excluded, because it is a stray
// delimiter fragment, never a real dedent back to a new top-level construct.
// Without this, that fragment's zero leading whitespace reads as "the
// function ended", truncating the body before it ever reaches what the scan
// is actually looking for.
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
// test's loop is a NESTED declaration, not a new top-level test — ending
// there would truncate the outer test right before the loop that carries its
// actual assertion, the single most common Go test shape in this repo. No
// ending line found at all (the function runs to the end of what this edit
// shows) returns everything from i to EOF. Blank lines never end the body —
// an intervening blank inside a table-driven test is not a dedent. Reused by
// panicOnlyOraclePolicy below to bound both a Fuzz/Test function's own body
// and, recursively, its deferred recover closure's body.
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

// hasPanicOnlyOracle reports whether masked source declares a Go
// Test/Benchmark/Fuzz/Example function (or a t.Run subtest) whose ONLY
// assertion sits inside a defer/recover panic-catcher, while some OTHER call
// in the same body has its result discarded: the function proves the code
// under test does not panic and asserts nothing about what it computes.
// Go-scoped only — recover() and the blank-identifier discard idiom are
// Go's own; the evidence for this shape (issue #319's incident 8) is
// entirely Go.
func hasPanicOnlyOracle(masked string) bool {
	lines := strings.Split(masked, "\n")
	for i, line := range lines {
		if !isGoTestDeclLine(line) {
			continue
		}
		if panicOnlyOracleBody(strings.Split(testFunctionBody(lines, i), "\n")) {
			return true
		}
	}
	return false
}

// isGoTestDeclLine reports whether line declares a Go test the way
// fail-first's declaration table judges it (testDeclLine), TestMain excluded,
// reused rather than redefined.
func isGoTestDeclLine(line string) bool {
	decl, _ := testDeclLine(".go", line)
	return decl
}

// panicOnlyOracleBody judges one function's body (already sliced by
// testFunctionBody, so index 0 is the function's own declaration line):
// true when the function's only assertion token lives inside a
// defer/recover block, AND some line OUTSIDE that block discards a call's
// result. Either condition failing means this is not the shape: a real
// assertion outside the recover block means the function does check
// something, and no discard outside it means there is nothing left
// unchecked — the legitimate case of a fuzz target whose function under
// test returns nothing at all.
func panicOnlyOracleBody(body []string) bool {
	deferAt := -1
	for i, l := range body {
		if goDeferRecoverRe.MatchString(l) {
			deferAt = i
			break
		}
	}
	if deferAt < 0 {
		return false
	}
	deferBody := strings.Split(testFunctionBody(body, deferAt), "\n")
	deferEnd := deferAt + len(deferBody) // exclusive, in body's own indices
	recovers := false
	for _, l := range deferBody {
		if recoverCallRe.MatchString(l) {
			recovers = true
			break
		}
	}
	if !recovers {
		return false
	}
	sawAssertionInRecover := false
	discardOutsideRecover := false
	for i, l := range body {
		inRecover := i >= deferAt && i < deferEnd
		if goAssertionTokenRe.MatchString(l) {
			if !inRecover {
				return false // a real assertion lives outside the panic-catcher
			}
			sawAssertionInRecover = true
		}
		if !inRecover && goDiscardCallRe.MatchString(l) {
			discardOutsideRecover = true
		}
	}
	return sawAssertionInRecover && discardOutsideRecover
}
