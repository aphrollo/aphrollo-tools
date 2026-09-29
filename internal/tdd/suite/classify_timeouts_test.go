package suite

import "testing"

// The outputs below are captured from real runs (nextest 0.9.143, go 1.26,
// vitest 5.0.2, jest 30), trimmed of lines no reader here looks at.

const nextestTimeoutsOnlyOut = `        PASS [   0.008s] (1/3) tmo tests::quick
        SLOW [>  1.000s] (───) tmo tests::slow_two
 TERMINATING [>  2.000s] (───) tmo tests::slow_one
     TIMEOUT [   2.003s] (2/3) tmo tests::slow_one
  Cancelling due to test failure: 1 test still running
     TIMEOUT [   2.004s] (3/3) tmo tests::slow_two
────────────
     Summary [   2.004s] 3 tests run: 1 passed, 2 timed out, 0 skipped
     TIMEOUT [   2.003s] (2/3) tmo tests::slow_one
     TIMEOUT [   2.004s] (3/3) tmo tests::slow_two
error: test run failed
`

const nextestFailAndTimeoutOut = `        FAIL [   0.016s] (1/3) tmo tests::quick
     TIMEOUT [   2.008s] (2/3) tmo tests::slow_two
     Summary [   2.014s] 3 tests run: 0 passed, 1 failed, 1 timed out, 0 skipped
        FAIL [   0.016s] (1/3) tmo tests::quick
     TIMEOUT [   2.008s] (2/3) tmo tests::slow_two
error: test run failed
`

const nextestFailOnlyOut = `        FAIL [   0.016s] (1/1) tmo tests::quick
     Summary [   0.016s] 1 test run: 0 passed, 1 failed, 0 skipped
error: test run failed
`

const goTimeoutOnlyOut = `=== RUN   TestSlow
panic: test timed out after 2s
	running tests:
		TestSlow (2s)

goroutine 8 [running]:
testing.(*M).startAlarm.func1()
FAIL	ex/a	2.006s
=== RUN   TestOK
--- PASS: TestOK (0.00s)
PASS
ok  	ex/b	0.003s
FAIL
`

const goTimeoutAndAssertionOut = `=== RUN   TestSlow
panic: test timed out after 2s
	running tests:
		TestSlow (2s)
FAIL	ex/a	2.006s
=== RUN   TestBad
    b_test.go:3: want 1
--- FAIL: TestBad (0.00s)
FAIL	ex/b	0.003s
FAIL
`

const goTimeoutAndBuildFailureOut = `panic: test timed out after 2s
	running tests:
		TestSlow (2s)
FAIL	ex/a	2.006s
# ex/c
c/c.go:3:1: syntax error: non-declaration statement outside function body
FAIL	ex/c [build failed]
FAIL
`

const goFailureThenTimeoutInOnePackageOut = `=== RUN   TestBad
    a_test.go:3: want 1
--- FAIL: TestBad (0.00s)
=== RUN   TestSlow
panic: test timed out after 2s
	running tests:
		TestSlow (2s)
FAIL	ex/a	2.007s
FAIL
`

const vitestTimeoutsOnlyOut = ` ❯ a.test.js (2 tests | 1 failed) 328ms
   × slow one 320ms
 ❯ d.test.js (1 test | 1 failed) 320ms
   × slow two 317ms

⎯⎯⎯⎯⎯⎯⎯ Failed Tests 2 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  a.test.js > slow one
Error: Test timed out in 300ms.
If this is a long-running test, pass a timeout value as the last argument or configure it globally with "testTimeout".
 ❯ a.test.js:2:1

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/2]⎯

 FAIL  d.test.js > slow two
Error: Test timed out in 300ms.
 ❯ d.test.js:2:1

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[2/2]⎯

 Test Files  2 failed | 1 passed (3)
      Tests  2 failed | 2 passed (4)
`

const vitestTimeoutAndAssertionOut = `⎯⎯⎯⎯⎯⎯⎯ Failed Tests 2 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  a.test.js > slow one
Error: Test timed out in 300ms.
 ❯ a.test.js:2:1

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/2]⎯

 FAIL  c.test.js > bad
AssertionError: expected 1 to be 2 // Object.is equality
 ❯ c.test.js:2:31

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[2/2]⎯

 Test Files  2 failed | 1 passed (3)
      Tests  2 failed | 2 passed (4)
`

const vitestTimeoutAndUnhandledErrorOut = ` FAIL  a.test.js > slow one
Error: Test timed out in 300ms.
 ❯ a.test.js:2:1

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/1]⎯

⎯⎯⎯⎯⎯⎯ Unhandled Errors ⎯⎯⎯⎯⎯⎯

Vitest caught 1 unhandled error during the test run.
TypeError: Cannot read properties of undefined (reading 'x')

 Test Files  1 failed (1)
      Tests  1 failed | 1 passed (2)
`

const jestTimeoutsOnlyOut = `FAIL ./a.test.js
  ● slow one

    thrown: "Exceeded timeout of 300 ms for a test.
    Add a timeout value to this test to increase the timeout, if this is a long-running test. See https://jestjs.io/docs/api#testname-fn-timeout."

    Cause:
        thrown: "Exceeded timeout of 300 ms for a test.

FAIL ./b.test.js
  ● slow two

    thrown: "Exceeded timeout of 300 ms for a test.

Test Suites: 2 failed, 2 total
Tests:       2 failed, 1 passed, 3 total
`

const jestTimeoutAndAssertionOut = `FAIL ./a.test.js
  ● slow one

    thrown: "Exceeded timeout of 300 ms for a test.

FAIL ./c.test.js
  ● bad

    expect(received).toBe(expected) // Object.is equality

Test Suites: 2 failed, 2 total
Tests:       2 failed, 1 passed, 3 total
`

// TestRunnerTimeoutsOnly_ReadsEachRunnersOwnTimeoutShape pins the one
// question issue #945 turns on: did every test this run failed fail by
// running out of time? Only then is the run inconclusive rather than red. A
// single real failure beside the timeouts keeps it red, whichever runner.
func TestRunnerTimeoutsOnly_ReadsEachRunnersOwnTimeoutShape(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"nextest, two timeouts and no failure", nextestTimeoutsOnlyOut, true},
		{"nextest, a failure beside a timeout", nextestFailAndTimeoutOut, false},
		{"nextest, a failure alone", nextestFailOnlyOut, false},
		{"go, one package timed out and the other passed", goTimeoutOnlyOut, true},
		{"go, a timeout beside an assertion failure", goTimeoutAndAssertionOut, false},
		{"go, a timeout beside a package that did not build", goTimeoutAndBuildFailureOut, false},
		{"go, a failure then a timeout in one package", goFailureThenTimeoutInOnePackageOut, false},
		{"go, an assertion failure alone", "--- FAIL: TestBad (0.00s)\nFAIL\tex/b\t0.003s\nFAIL\n", false},
		{"vitest, two timed-out tests", vitestTimeoutsOnlyOut, true},
		{"vitest, a timeout beside an assertion failure", vitestTimeoutAndAssertionOut, false},
		{"vitest, a timeout beside an unhandled error", vitestTimeoutAndUnhandledErrorOut, false},
		{"jest, two timed-out tests", jestTimeoutsOnlyOut, true},
		{"jest, a timeout beside an assertion failure", jestTimeoutAndAssertionOut, false},
		{"a compile error", "error[E0425]: cannot find function `widget` in this scope\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runnerTimeoutsOnly(c.output); got != c.want {
				t.Fatalf("runnerTimeoutsOnly = %v, want %v for:\n%s", got, c.want, c.output)
			}
		})
	}
}
