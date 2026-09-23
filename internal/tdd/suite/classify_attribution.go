package suite

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A clean RED is a claim about a TEST: the test names a symbol nobody has
// written yet, so the next move is the implementation. The phrase alone does
// not say whose symbol it is. Deleting an unused-looking const from a source
// file that another source file still reads produces rustc's E0425, the very
// diagnostic a test calling a missing function produces, and the hook told
// the session to "write the minimum implementation" for an edit that had
// simply broken the build (issue #759). A compiler diagnostic always says
// WHERE the unresolved name is used, so for the two toolchains whose
// diagnostics carry a location — go and cargo — the claim is kept only when
// that place is test code.

// goZigDiagLocRe is a go/zig compiler diagnostic's own location: the path at
// the very start of the line, then line and column. A test's own log line
// ("    x_test.go:40: got ...") is indented, so an assertion message that
// quotes a missing-symbol phrase never reads as a compiler diagnostic.
var goZigDiagLocRe = regexp.MustCompile(`^(\S+?\.(?:go|zig)):(\d+)(?::\d+)?: `)

// rustDiagLocRe is rustc's location line under an `error[E…]` header.
var rustDiagLocRe = regexp.MustCompile(`^\s*--> (\S+?):(\d+):\d+`)

// attributedToolchains are the runners whose missing-symbol diagnostics
// always carry a location, so an unlocated phrase in their output is not a
// compiler diagnostic at all.
var attributedToolchains = map[string]bool{"go": true, "cargo": true}

// attributeMissingImpl keeps a RedMissingImpl verdict only when one of the
// run's missing-symbol diagnostics is located in test code, and answers Red
// otherwise. Every other outcome, and every runner outside
// attributedToolchains, passes through unchanged.
func attributeMissingImpl(outcome Outcome, r Runner, root, output string) Outcome {
	if outcome != RedMissingImpl || !attributedToolchains[r.Cmd] {
		return outcome
	}
	if missingSymbolInTestCode(output, runnerDir(r, root)) {
		return outcome
	}
	return Red
}

// missingSymbolInTestCode reports whether any missing-symbol diagnostic in
// output is located in test code. dir is where the run executed, which is
// what a relative location is relative to: cargo reports paths from the
// workspace root it ran in.
//
// go and zig print the location at the start of the diagnostic's own line;
// rustc prints it on the `-->` line directly beneath the `error[E…]` header,
// so each line is read together with the one above it.
func missingSymbolInTestCode(output, dir string) bool {
	prev := ""
	for line := range strings.SplitSeq(output, "\n") {
		header := prev
		prev = line
		m := goZigDiagLocRe.FindStringSubmatch(line)
		if m == nil || !missingImplRe.MatchString(line) {
			m = rustDiagLocRe.FindStringSubmatch(line)
			if m == nil || !missingImplRe.MatchString(header) {
				continue
			}
		}
		n, _ := strconv.Atoi(m[2])
		if isTestCodeAt(m[1], n, dir) {
			return true
		}
	}
	return false
}

// isTestCodeAt reports whether line lineNo of file is test code: a file the
// gate classifies as a test, a Rust file a #[cfg(test)] module mounts, or a
// line inside a #[cfg(test)] item of a Rust source file.
func isTestCodeAt(file string, lineNo int, dir string) bool {
	file = filepath.FromSlash(strings.ReplaceAll(file, `\`, "/"))
	if ClassifyFile(file) == Test {
		return true
	}
	if !strings.EqualFold(filepath.Ext(file), ".rs") {
		return false
	}
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dir, file)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return false // absence-ok: a location this cannot read is not provably test code
	}
	return rustLineIsTestCode(dir, abs, string(data), lineNo)
}

// rustLineIsTestCode answers isTestCodeAt for one Rust file read from disk.
func rustLineIsTestCode(dir, abs, src string, lineNo int) bool {
	if rel, err := filepath.Rel(dir, abs); err == nil && rustFileIsTestModule(dir, filepath.ToSlash(rel), abs) {
		return true
	}
	class, ok := rustLex(src)
	if !ok {
		return false
	}
	spans, ok := rustCfgTestSpans(rustMasked(src, class))
	if !ok {
		return false
	}
	return inSpans(spans, lineOffset(src, lineNo))
}

// lineOffset is the byte offset where 1-based line n of src starts, len(src)
// when src has fewer lines.
func lineOffset(src string, n int) int {
	off := 0
	for k, line := range strings.SplitAfter(src, "\n") {
		if k+1 == n {
			return off
		}
		off += len(line)
	}
	return len(src)
}
