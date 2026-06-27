package tdd

import "testing"

// blocks reports whether content trips a blocking smell, for terse assertions.
func blocks(content string) bool { return smellCheck(content).Action == Block }

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
