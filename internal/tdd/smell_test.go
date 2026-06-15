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
		"// assert x == x",          // self-compare only in a comment
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
		"model.fit(data)",      // .fit method call, not the fit() alias
		"prefit(data)",         // identifier ending in fit
		"context.only = 5",     // property assignment, not a focused call
		"// it.only is banned", // mention in a comment
		`s = "use fit() for focus"`, // mention in a string
		`log("say \"it.only(\" now")`, // escaped quotes must not leak the marker as code
		"it('x', () => {})",    // ordinary test
	}
	for _, src := range allowed {
		if blocks(src) {
			t.Errorf("false focused-test block for legitimate %q", src)
		}
	}
}

func TestSmell_TestSleep(t *testing.T) {
	blocked := []string{
		"time.Sleep(2 * time.Second)",
		"time.sleep(5)",
		"asyncio.sleep(1)",
		"Thread.sleep(100)",
		"setTimeout(done, 500)",
		"std::thread::sleep(d)",
	}
	for _, src := range blocked {
		if !blocks(src) {
			t.Errorf("expected sleep block for %q", src)
		}
	}

	allowed := []string{
		"// time.Sleep(2) is flaky",  // comment
		`log("time.sleep(5)")`,       // string
		"sleepCount += 1",            // identifier containing 'sleep'
		"clock.Advance(time.Second)", // fake clock, not a real sleep
	}
	for _, src := range allowed {
		if blocks(src) {
			t.Errorf("false sleep block for legitimate %q", src)
		}
	}
}
