package tdd

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// A Go run says PER PACKAGE whether it ran a test: in its -json events (a
// per-test pass/fail/skip event, attributed to its package) and in its own
// summary lines ("ok" for a package whose tests ran, "?  [no test files]"
// and "ok … [no tests to run]" for one where none did). ClassifyOutcome
// reads the whole output as one text and calls a run writing-test when ANY
// of it says "no test files", so one test-less package in a multi-package
// run turned every real pass beside it into scaffolding. classifyRunOutcome
// decides a passing Go run by that per-package attribution instead:
// writing-test only when no package ran a test.

// classifyRunOutcome maps one finished run to an Outcome. A passing Go run
// whose packages can be read is judged per package; everything else goes to
// ClassifyOutcome unchanged.
func classifyRunOutcome(r Runner, res SuiteResult, prevFailing []string) Outcome {
	output := classificationOutput(res.Output, res.GoTestJSON)
	if res.Passed && r.Cmd == "go" {
		if ran, known := goRunRanATest(res); known {
			switch {
			case !ran:
				return WritingTest
			case warningRe.MatchString(goNoTestsNoteRe.ReplaceAllString(output, "")):
				return GreenWithWarnings
			default:
				return Green
			}
		}
	}
	return ClassifyOutcome(res.Passed, output, prevFailing)
}

// goNoTestsNoteRe is go test's own note for a package that ran no test. It
// carries the word "warning:" but warns about nothing in the code, so a
// passing package beside it is not green-with-warnings on its account.
var goNoTestsNoteRe = regexp.MustCompile(`(?m)^testing: warning: no tests to run\r?\n?`)

// goPackageResultRe matches `go test`'s one summary line per package:
// "ok", "?" or "FAIL", the import path, then the rest.
var goPackageResultRe = regexp.MustCompile(`(?m)^(?:ok|\?|FAIL)[ \t]+\S+[ \t].*$`)

// goRunRanATest reports whether any package in a Go run ran a test, and
// whether that could be read at all: from the -json stream when the run has
// one, else from the per-package summary lines. known is false for a stream
// that does not decode to the end, or output with no package line — neither
// may be read as "no test ran".
func goRunRanATest(res SuiteResult) (ran, known bool) {
	if res.GoTestJSON != "" {
		return goJSONRanATest(res.GoTestJSON)
	}
	lines := goPackageResultRe.FindAllString(res.Output, -1)
	if len(lines) == 0 {
		return false, false
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "ok") && !strings.Contains(l, "[no tests to run]") {
			return true, true
		}
	}
	return false, true
}

// goJSONRanATest is goRunRanATest over a -json stream: a per-test pass,
// fail or skip event in any package is a test that ran, the same attribution
// vacuousGoPackages uses.
func goJSONRanATest(raw string) (ran, known bool) {
	dec := json.NewDecoder(strings.NewReader(raw))
	for {
		var e goTestEvent
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				return ran, known
			}
			return false, false
		}
		if e.Package == "" {
			continue
		}
		known = true
		if e.Test != "" && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip") {
			ran = true
		}
	}
}

// goTopLevelPassRe matches a top-level test's pass line in a Go run's -v
// shaped output (what renderGoTestJSON reconstructs); a subtest's line is
// indented and is not a test of its own.
var goTopLevelPassRe = regexp.MustCompile(`(?m)^--- PASS: `)

// goPassedCount is the Go half of parsePassedCount: the number of top-level
// tests that passed, when the output shows any.
func goPassedCount(output string) (int, bool) {
	n := len(goTopLevelPassRe.FindAllStringIndex(output, -1))
	return n, n > 0
}
