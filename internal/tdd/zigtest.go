package tdd

import (
	"regexp"
	"strings"
)

// zigTestBlockRe locates the opening brace of a Zig `test` declaration in
// MASKED code: the `test` keyword (bounded so `mytest`/`testing`/`foo.test`
// don't match), an optional name — a string `test "..."` or a bare identifier
// `test name` — then `{`. The name's content is already blanked by masking, so
// a `{`/`}` inside a test description can't be mistaken for block structure, and
// `[^{}\n]*` between the keyword and the brace can cross neither a brace nor a
// newline, so the match cannot leap into an unrelated block.
var zigTestBlockRe = regexp.MustCompile(`(?:^|[^\w.])test\b[^{}\n]*\{`)

// zigTestLines returns the 1-based line numbers that fall INSIDE an inline Zig
// `test "..." { ... }` (or `test name { ... }`) block, brace-matched so nested
// braces close the right block. It operates on the already-masked code view, so
// string/comment braces are blanked and can't fool the matcher.
//
// The span is inclusive of the opening and closing brace lines: a single-line
// `test "x" { ... }` puts the whole body on the opener line, and a smell can sit
// on the same physical line as the closing `}`, so both ends must be kept. The
// `test "..." {` prefix and a trailing `}` are harmless — neither trips a smell.
//
// This is how the oracle-smell gate reaches inline tests in a Source-classified
// .zig file WITHOUT touching the surrounding production code: only these lines
// are handed to the smell detectors (see DecidePreEdit's Source branch). Restrict
// a view to them with keepLines.
func zigTestLines(masked string) map[int]bool {
	keep := map[int]bool{}
	for _, loc := range zigTestBlockRe.FindAllStringIndex(masked, -1) {
		open := loc[1] - 1 // index of the `{` the regex ends on
		end := matchBrace(masked, open)
		if end < 0 {
			continue // unbalanced (mid-edit fragment) — keep nothing rather than over-reach
		}
		for ln := lineOf(masked, open); ln <= lineOf(masked, end); ln++ {
			keep[ln] = true
		}
	}
	return keep
}

// matchBrace returns the index of the `}` that closes the `{` at open, or -1 if
// the braces are unbalanced. masked input means braces in strings/comments are
// already blanked, so the depth count only sees real structural braces.
func matchBrace(masked string, open int) int {
	depth := 0
	for i := open; i < len(masked); i++ {
		switch masked[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// lineOf returns the 1-based line number of byte index idx in s.
func lineOf(s string, idx int) int {
	return strings.Count(s[:idx], "\n") + 1
}
