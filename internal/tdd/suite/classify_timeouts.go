package suite

import "regexp"

// A runner can end a test at its own per-test deadline and still finish the
// run: nextest's slow-timeout, go test's -timeout panic, vitest's
// testTimeout and jest's timeout. The run exits non-zero, but a test that
// ran out of time says nothing about the code under it, the same as a run
// the gate killed at its own deadline (issue #945). runnerTimeoutsOnly
// recognises the run whose every failure is such a timeout, so post-edit
// logs it as the inconclusive TIMEOUT it is instead of a red, and the one
// targeted rerun a TIMEOUT sanctions is not then refused over a red verdict
// no test earned. One real failure beside the timeouts keeps the run red.

// nextestSummaryLineRe is nextest's one `Summary [ <secs>] <n> tests run: …`
// line, which counts every status the run ended with.
var nextestSummaryLineRe = regexp.MustCompile(`(?m)^[^\S\n]*Summary[^\S\n]+\[[^\]\n]*\][^\n]*`)

// nextestTimedOutRe is that line's timed-out count, when there is one.
var nextestTimedOutRe = regexp.MustCompile(`\b[1-9]\d* timed out\b`)

// failedWordRe is any failed count on the summary line: `1 failed`,
// `1 exec failed`, `1 leak-failed`.
var failedWordRe = regexp.MustCompile(`\bfailed\b`)

// goTimeoutPanicRe is the panic go test's own -timeout alarm raises, once
// per package binary that ran out of time.
var goTimeoutPanicRe = regexp.MustCompile(`(?m)^panic: test timed out after `)

// goPackageFailRe is go test's per-package FAIL summary line, a build
// failure's `FAIL <pkg> [build failed]` included; the bare closing `FAIL`
// names no package and does not match.
var goPackageFailRe = regexp.MustCompile(`(?m)^FAIL[ \t]+\S`)

// goOwnTestFailRe is a test that failed on its own account.
var goOwnTestFailRe = regexp.MustCompile(`(?m)^[ \t]*--- FAIL:`)

// jsFailedTestRe is one failed test's header: vitest's ` FAIL  <file> > <name>`
// and jest's `  ● <name>`. Jest's per-file `FAIL ./<file>` carries one space
// and is not a test.
const jsFailedTestRe = `[^\S\n]*(?:FAIL  \S|● )[^\n]*\n`

// jsTimedOutTestRe is a failed test's header (vitest may list several tests
// sharing one error) followed, past blank lines, by the timeout message as
// the error's first line.
var jsTimedOutTestRe = regexp.MustCompile(`(?m)^(?:` + jsFailedTestRe + `|[^\S\n]*\n)*[^\S\n]*(?:Error: (?:Test|Hook) timed out in \d+ms|thrown: "Exceeded timeout of \d+ ms for a (?:test|hook))`)

// jsAnyFailedTestRe is any failed test's header.
var jsAnyFailedTestRe = regexp.MustCompile(`(?m)^` + jsFailedTestRe)

// jsUnhandledErrorRe is vitest's section for an error no test owns.
var jsUnhandledErrorRe = regexp.MustCompile(`Unhandled Error`)

// runnerTimeoutsOnly reports whether output is a finished run whose every
// failed test failed by timing out, as nextest, go test, vitest or jest
// reports it.
func runnerTimeoutsOnly(output string) bool {
	output = ansiSGRRe.ReplaceAllString(output, "")
	return nextestTimeoutsOnly(output) || goTimeoutsOnly(output) || jsTimeoutsOnly(output)
}

func nextestTimeoutsOnly(output string) bool {
	summary := nextestSummaryLineRe.FindString(output)
	return nextestTimedOutRe.MatchString(summary) && !failedWordRe.MatchString(summary)
}

// goTimeoutsOnly holds when every failed package failed by the timeout
// panic and no test failed on its own.
func goTimeoutsOnly(output string) bool {
	panics := len(goTimeoutPanicRe.FindAllStringIndex(output, -1))
	failed := len(goPackageFailRe.FindAllStringIndex(output, -1))
	return panics > 0 && panics == failed && !goOwnTestFailRe.MatchString(output)
}

// jsTimeoutsOnly holds when removing every timed-out test leaves no failed
// test behind, and at least one was removed.
func jsTimeoutsOnly(output string) bool {
	rest := jsTimedOutTestRe.ReplaceAllString(output, "")
	return rest != output && !jsAnyFailedTestRe.MatchString(rest) && !jsUnhandledErrorRe.MatchString(rest)
}
