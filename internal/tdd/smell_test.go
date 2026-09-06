package tdd

import "testing"

// ratchet: test_removed TestSmell_AssertionFree: assertion-free is dropped, 0 of 8 #319 incidents caught, measurement recorded on the issue

// blocks reports whether content trips a blocking smell, for terse assertions.
func blocks(content string) bool { return smellCheck(content).Action == Block }

// oracleWarnAction reports the action testOracleWarnings takes on content at a
// given phase — the same shape suppressAt (suppress_test.go) uses for the
// any-code-file suppressions, mirrored here for the test-scoped ones.
func oracleWarnAction(content string, p phase) Action {
	return evaluate(content, testOracleWarnings, p, defaultLang).Action
}

func TestSmell_Tautology(t *testing.T) {
	blocked := []string{
		"assert x == x",
		"assert user.name == user.name",
		"assert arr[0] == arr[0]",
		"assert 5 == 5",
		"expect(value).toBe(value)",
		"expect(user.id).toEqual(user.id)",
		"assert.strictEqual(result, result)",
		"assert.equal(x, x)",
		// Single-operand forms: the literal IS the second operand, so there is
		// really only one value in play — just as vacuous as comparing a
		// value to itself.
		"assert.True(t, true)",
		"require.True(t, true)",
		"assert!(true)",
		"assert True",
		"assert True,",
		"expect(true).toBe(true)", // already covered by the two-operand path (both captures are the literal "true")
	}
	for _, src := range blocked {
		if !blocks(src) {
			t.Errorf("expected tautology block for %q", src)
		}
	}

	// Legitimate assertions that must NOT be blocked — the call-exclusion fix.
	allowed := []string{
		"expect(fn()).toBe(fn())",       // calls can return different values
		"assert len(a) == len(a)",       // call operands excluded
		"expect(actual).toBe(expected)", // distinct operands
		"assert x == y",
		"assert.equal(actual, expected)",
		"// assert x == x",           // self-compare only in a comment
		`msg = "assert x == x here"`, // self-compare only in a string
		"expect(a).toBe(b)",
		"assert.True(t, isValid)",  // a real variable, not the hardcoded literal
		"assert True == checkOK()", // a genuine (if oddly written) comparison, not a bare literal
		"assert False",             // a different literal entirely
	}
	for _, src := range allowed {
		if blocks(src) {
			t.Errorf("false tautology block for legitimate %q", src)
		}
	}
}

func TestSmell_FocusedTest(t *testing.T) {
	blocked := []string{
		"it.only('x', () => {})",
		"describe.only('suite', () => {})",
		"context.only('ctx', fn)",
		"fit('x', () => {})",
		"fdescribe('suite', () => {})",
	}
	for _, src := range blocked {
		if !blocks(src) {
			t.Errorf("expected focused-test block for %q", src)
		}
	}

	allowed := []string{
		"model.fit(data)",             // .fit method call, not the fit() alias
		"prefit(data)",                // identifier ending in fit
		"context.only = 5",            // property assignment, not a focused call
		"// it.only is banned",        // mention in a comment
		`s = "use fit() for focus"`,   // mention in a string
		`log("say \"it.only(\" now")`, // escaped quotes must not leak the marker as code
		"it('x', () => {})",           // ordinary test
	}
	for _, src := range allowed {
		if blocks(src) {
			t.Errorf("false focused-test block for legitimate %q", src)
		}
	}
}

func TestSmell_DisabledTest(t *testing.T) {
	blocked := []string{
		"it.skip('x', () => {})",
		"describe.skip('suite', fn)",
		"test.skip('x', fn)",
		"xit('x', () => {})",
		"xdescribe('suite', fn)",
		"func TestX(t *testing.T) { t.Skip() }",
		"func TestX(t *testing.T) { t.Skipf(why) }",
		"func BenchmarkX(b *testing.B) { b.SkipNow() }",
		"@pytest.mark.skip / def test_x",
		"@pytest.mark.skipif(cond) / def test_x",
		"@unittest.skip('reason') / def test_x",
	}
	for _, src := range blocked {
		if !blocks(src) {
			t.Errorf("expected disabled-test block for %q", src)
		}
	}

	allowed := []string{
		"reader.Skip(4)",            // domain method, long receiver — not t.Skip
		"obj.xit(data)",             // member access, not the xit() alias
		"it('x', () => {})",         // ordinary test
		"// it.skip is banned",      // mention in a comment
		`s = "use it.skip to skip"`, // mention in a string
		"results.skip = true",       // property assignment, no call
	}
	for _, src := range allowed {
		if blocks(src) {
			t.Errorf("false disabled-test block for legitimate %q", src)
		}
	}
}

func TestSmell_Zig(t *testing.T) {
	blocked := []string{
		// sleep — Zig's real-time sleeps.
		"std.time.sleep(100 * std.time.ns_per_ms)",
		"std.Thread.sleep(1_000_000)",
		// disabled-test — Zig skips a test by returning this error.
		"if (!ready) return error.SkipZigTest;",
		"    return error.SkipZigTest;",
		// tautology — self-compare via expectEqual.
		"try std.testing.expectEqual(x, x)",
		"try expectEqual(user.id, user.id)",
		"try std.testing.expectEqualStrings(name, name)",
	}
	for _, src := range blocked {
		if !blocks(src) {
			t.Errorf("expected Zig smell block for %q", src)
		}
	}

	allowed := []string{
		"std.time.nanoTimestamp()",                      // a timer read, not a sleep
		"return error.OutOfMemory;",                     // a different error, not a skip
		"// return error.SkipZigTest; (disabled below)", // skip only in a comment
		`const msg = "return error.SkipZigTest";`,       // skip only in a string
		"try std.testing.expectEqual(expected, actual)", // distinct operands
		"try expectEqual(@as(i32, 3), add(1, 2))",       // distinct (and call operands excluded)
		"try std.testing.expectEqualStrings(want, got)", // distinct operands
	}
	for _, src := range allowed {
		if blocks(src) {
			t.Errorf("false Zig smell block for legitimate %q", src)
		}
	}
}

func TestSmell_TestSleep(t *testing.T) {
	blocked := []string{
		"time.Sleep(2 * time.Second)",
		"time.sleep(5)", // Python sync sleep (time.[Ss]leep covers it)
		"asyncio.sleep(1)",
		"Thread.sleep(100)",
		"setTimeout(done, 500)",
		"std::thread::sleep(d)",
		// Go channel-based real-time waits — just as real-time as Sleep.
		"<-time.After(5 * time.Second)",
		"case <-time.After(time.Second):", // the idiomatic select-timeout fixture
		"time.NewTimer(2 * time.Second)",  // constructor alone is the marker (no `<-` needed)
		"<-time.Tick(time.Second)",
		"time.Tick(50 * time.Millisecond)",
		// JS/TS promisified sleep — the setTimeout inside the Promise wrapper trips it.
		"await new Promise((resolve) => setTimeout(resolve, 500))",
		"await new Promise(r => setTimeout(r, ms))",
	}
	for _, src := range blocked {
		if !blocks(src) {
			t.Errorf("expected sleep block for %q", src)
		}
	}

	allowed := []string{
		"// time.Sleep(2) is flaky",       // comment
		`log("time.sleep(5)")`,            // string
		"sleepCount += 1",                 // identifier containing 'sleep'
		"clock.Advance(time.Second)",      // fake clock, not a real sleep
		"// <-time.After(5) is a wait",    // channel wait only in a comment
		`s := "case <-time.After(d):"`,    // channel wait only in a string
		"time.AfterFunc(d, cb)",           // schedules a callback, does not block the goroutine
		"time.NewTicker(time.Second)",     // ticker construction, not the Tick() wait func
		"await new Promise(r => r(data))", // a real promise, no setTimeout — not a sleep
	}
	for _, src := range allowed {
		if blocks(src) {
			t.Errorf("false sleep block for legitimate %q", src)
		}
	}
}

// TestSmell_PanicOnlyOracle covers issue #319's incident 8 directly: a
// Fuzz/Test function whose only assertion is a defer/recover panic-catcher,
// with the function under test's return value discarded elsewhere in the
// body. Mirrors FuzzCargoShimArgv/FuzzGitShimArgv/FuzzBashWriteTargets/
// FuzzReceipt as they stood at 76f9c48~1, before that commit fixed all four.
func TestSmell_PanicOnlyOracle(t *testing.T) {
	hit := []string{
		// FuzzCargoShimArgv's shape, minimally: the return value of the call
		// under test discarded, no other assertion anywhere.
		"func FuzzCargoShimArgv(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, blob string) {\n" +
			"\t\tdefer func() {\n\t\t\tif r := recover(); r != nil {\n\t\t\t\tt.Fatalf(\"panicked: %v\", r)\n\t\t\t}\n\t\t}()\n" +
			"\t\targs := argvFromBlob(blob)\n\t\t_ = cargoVerb(args)\n\t\t_ = cargoRunArgsToBuildArgs(args)\n\t})\n}",
		// FuzzGitShimArgv's shape: a tuple discard (`_, rest :=`) still counts.
		"func FuzzGitShimArgv(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, blob string) {\n" +
			"\t\tdefer func() {\n\t\t\tif r := recover(); r != nil {\n\t\t\t\tt.Fatalf(\"panicked: %v\", r)\n\t\t\t}\n\t\t}()\n" +
			"\t\targs := argvFromBlob(blob)\n\t\t_, rest := gitGlobalArgs(args)\n\t\t_ = isPlainMerge(rest)\n\t})\n}",
	}
	for _, src := range hit {
		if got := oracleWarnAction(src, editPhase); got != Warn {
			t.Errorf("edit phase: got %v for %q, want Warn", got, src)
		}
		if got := oracleWarnAction(src, commitPhase); got != Block {
			t.Errorf("commit phase: got %v for %q, want Block", got, src)
		}
	}

	allowed := []string{
		// A real assertion OUTSIDE the recover block: this is FuzzCargoShimArgv
		// as 76f9c48 actually fixed it — checked, not panic-only anymore.
		"func FuzzCargoShimArgv(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, blob string) {\n" +
			"\t\tdefer func() {\n\t\t\tif r := recover(); r != nil {\n\t\t\t\tt.Fatalf(\"panicked: %v\", r)\n\t\t\t}\n\t\t}()\n" +
			"\t\targs := argvFromBlob(blob)\n\t\trewritten := cargoRunArgsToBuildArgs(args)\n" +
			"\t\tif len(rewritten) != len(args) {\n\t\t\tt.Fatalf(\"want %d, got %d\", len(args), len(rewritten))\n\t\t}\n\t})\n}",
		// A genuine void-returning smoke check: nothing is discarded because
		// there is nothing to discard — the legitimate case the discard
		// requirement exists to spare.
		"func FuzzWrite(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, data []byte) {\n" +
			"\t\tdefer func() {\n\t\t\tif r := recover(); r != nil {\n\t\t\t\tt.Fatalf(\"panicked: %v\", r)\n\t\t\t}\n\t\t}()\n" +
			"\t\twriteToBuffer(data)\n\t})\n}",
		// No defer/recover at all: an ordinary assertion-free-shaped test
		// (which this policy does not name — see git history) is out of scope
		// for panic-only-oracle specifically.
		"func TestAdd(t *testing.T) {\n\tgot := add(1, 2)\n\t_ = got\n}",
	}
	for _, src := range allowed {
		if got := oracleWarnAction(src, commitPhase); got != Allow {
			t.Errorf("false panic-only-oracle block for legitimate %q: got %v", src, got)
		}
	}
}

// TestSmell_ErrorKindBlind covers the four blind-check shapes named in the
// issue, each immunized by a safety-net token within two lines.
func TestSmell_ErrorKindBlind(t *testing.T) {
	hit := []string{
		"err := doThing()\nrequire.Error(t, err)",
		"err := doThing()\nassert.Error(t, err)",
		"let result = do_thing();\nassert!(result.is_err());",
		"with pytest.raises(Exception):\n    do_thing()",
		"expect(() => doThing()).toThrow()",
	}
	for _, src := range hit {
		if got := oracleWarnAction(src, editPhase); got != Warn {
			t.Errorf("edit phase: got %v for %q, want Warn", got, src)
		}
		if got := oracleWarnAction(src, commitPhase); got != Block {
			t.Errorf("commit phase: got %v for %q, want Block", got, src)
		}
	}

	allowed := []string{
		"err := doThing()\nrequire.ErrorIs(t, err, ErrNotFound)",
		"err := doThing()\nassert.ErrorContains(t, err, \"not found\")",
		"err := doThing()\nrequire.EqualError(t, err, \"not found\")",
		"let result = do_thing();\nassert!(result.is_err());\nassert!(matches!(result, Err(MyError::NotFound)));",
		"with pytest.raises(NotFoundError):\n    do_thing()",
		"with pytest.raises(Exception, match=\"not found\"):\n    do_thing()",
		"expect(() => doThing()).toThrow(NotFoundError)",
		"expect(() => doThing()).toThrow(\"not found\")",
		// A genuinely different assertion — no blind-check token at all.
		"got := doThing()\nrequire.Equal(t, want, got)",
	}
	for _, src := range allowed {
		if got := oracleWarnAction(src, commitPhase); got != Allow {
			t.Errorf("false error-kind-blind block for legitimate %q: got %v", src, got)
		}
	}
}
