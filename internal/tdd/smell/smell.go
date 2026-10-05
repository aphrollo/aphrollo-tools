package smell

import "github.com/aphrollo/aphrollo-tools/internal/oracle"

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

// The test-oracle smells, as policies. Each runs against the code view (strings
// and comments blanked) because every one matches executable test code, never a
// directive in a comment.
var (
	sleepPolicy = policy{
		name: "test-sleep", category: smellCat, reason: sleepReason,
		hit:    detects(oracle.TestSleep),
		escape: escapeSleep,
	}
	tautologyPolicy = policy{
		name: "tautology", category: smellCat, reason: tautologyReason,
		hit: detects(oracle.Tautology),
	}
	focusedPolicy = policy{
		name: "focused-test", category: smellCat, reason: focusedReason,
		hit: detects(oracle.FocusedTest),
	}
	disabledTestPolicy = policy{
		name: "disabled-test", category: smellCat, reason: disabledTestReason,
		hit:    detects(oracle.DisabledTest),
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
	hit:    detects(oracle.ErrorKindBlind),
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
	hit:    detects(oracle.PanicOnlyOracle),
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

// detects is a policy's predicate over the shared detector of its own name: the
// view the detector reads is the one the policy was handed.
func detects(detector string) func(v view) bool {
	return func(v view) bool {
		return oracle.Has(detector, oracle.Input{Code: v.Code, Directives: v.directives, Whole: v.whole, Rows: v.rows})
	}
}
