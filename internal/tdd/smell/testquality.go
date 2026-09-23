package smell

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Three judgement-call smells the oracle gate deliberately does NOT block on.
// Each one describes a test that runs, passes, and constrains far less than it
// appears to; none of them is certain enough to wedge an edit over, so they
// ride along as advice with a file:line the reader can jump to.

// weakBarRe is a sign check standing in for a value: `assert!(x > 0.0)` passes
// for a number 100x wrong, which is how a borld perpendicular-stiffness test
// asserted `> 1.0` and certified nothing.
var weakBarRe = regexp.MustCompile(`assert(?:_eq)?!\s*\([^)]*[<>]=?\s*-?0\.0`)

// genericNameRe matches names that describe no behaviour, so they cannot say
// which production change makes them red.
var genericNameRe = regexp.MustCompile(`fn\s+(?:test_\w+|\w*_works|\w*_basic|\w*smoke\w*)\s*\(`)

// toleranceRe matches an approximate comparison. A tolerance is a hole the
// size of the tolerance unless something says who needs one.
var toleranceRe = regexp.MustCompile(`approx_eq|abs_diff_eq|assert_relative_eq|<=?\s*EPS|<\s*1e-`)

// toleranceReasonRe is what silences it: a line naming the consumer.
var toleranceReasonRe = regexp.MustCompile(`//\s*(?:tolerance|why)\s*:`)

// physicsCrateRe names the crates whose numbers have closed forms — the
// Tier-1 predicted surface and the physics engine. Elsewhere a sign check is
// often exactly the right assertion.
var physicsCrateRe = regexp.MustCompile(`(?:^|[\\/])crates[\\/](forge\w*|movement|pose|shared)[\\/]`)

// QualityNotes reports the advisory notes for one test file's content,
// each as "<file>:<line>: <note>". Empty for a file that is not a Rust test,
// and for platform pins, which record what a MACHINE does — a sign bar or a
// tolerance there is the point of the file.
func QualityNotes(path, content string) []string {
	return qualityNotesOn(path, content, nil)
}

// qualityNotesOn is QualityNotes restricted to the 1-based lines in
// only; nil judges every line. Line numbers are always content's own, so a
// caller passing the whole file gets notes that name the file's lines.
func qualityNotesOn(path, content string, only map[int]bool) []string {
	if strings.ToLower(filepath.Ext(path)) != ".rs" || strings.Contains(path, "_platform_pin") {
		return nil
	}
	physics := physicsCrateRe.MatchString(path)
	lines := strings.Split(content, "\n")
	var notes []string
	for i, line := range lines {
		if only != nil && !only[i+1] {
			continue
		}
		note := ""
		switch {
		case physics && weakBarRe.MatchString(line):
			note = "weak bar: state the closed-form value and tolerance, not the sign"
		case genericNameRe.MatchString(line):
			note = "generic test name: name the production change that makes it red"
		case toleranceRe.MatchString(line) && !toleranceExplained(lines, i):
			note = "a tolerance names its consumer: add `// tolerance: <why>` above it"
		}
		if note != "" {
			notes = append(notes, fmt.Sprintf("%s:%d: %s", filepath.Base(path), i+1, note))
		}
	}
	return notes
}

// toleranceExplained reports whether one of the two lines above i says why a
// tolerance is needed, or one of the two lines above the assert macro whose
// argument list line i sits inside (issue #760): a multi-line `assert!(`
// carries its comparison several argument lines down, and the marker belongs
// above the statement, not inside the macro's arguments.
func toleranceExplained(lines []string, i int) bool {
	if reasonAbove(lines, i) {
		return true
	}
	if start, ok := enclosingAssert(lines, i); ok {
		return reasonAbove(lines, start)
	}
	return false
}

// reasonAbove reports whether one of the two lines above i names the
// tolerance's consumer.
func reasonAbove(lines []string, i int) bool {
	for _, line := range lines[max(0, i-2):i] {
		if toleranceReasonRe.MatchString(line) {
			return true
		}
	}
	return false
}

// assertStartRe opens an assertion macro's argument list.
var assertStartRe = regexp.MustCompile(`\bassert\w*!\s*\(`)

// enclosingAssert finds the line of the nearest assert macro above i whose
// argument list is still open at line i, by counting parentheses upward: a
// positive balance at an assert line means its `(` has not been closed
// before i. false when i sits inside no assert at all.
func enclosingAssert(lines []string, i int) (int, bool) {
	balance := 0
	for j, line := range slices.Backward(lines[:i]) {
		balance += strings.Count(line, "(") - strings.Count(line, ")")
		if balance > 0 && assertStartRe.MatchString(line) {
			return j, true
		}
	}
	return 0, false
}
