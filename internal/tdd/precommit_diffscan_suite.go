package tdd

import (
	"regexp"
)

var jsTestDeclRes = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(it|test|describe)(\.\w+)?\s*\(`),
}

// testDeclRes recognises an ADDED line that declares a test, per supported
// language. The set is deliberately declaration-shaped (attributes, test-func
// headers, subtest registrations) — an added assertion inside an existing test
// does not fire fail-first, matching its charter of proving NEW tests RED.
var testDeclRes = map[string][]*regexp.Regexp{
	".go": {
		regexp.MustCompile(`^\s*func\s+(Test|Benchmark|Fuzz|Example)\w*\s*\(`),
		regexp.MustCompile(`\bt\.Run\s*\(`),
	},
	".rs": {
		regexp.MustCompile(`^\s*#\[\s*(\w+(::\w+)*::)?(test|rstest|test_case)\b`),
	},
	".py": {
		regexp.MustCompile(`^\s*(async\s+)?def\s+test_`),
	},
	".zig": {
		regexp.MustCompile(`^\s*test\s+("|\{)`),
	},
	".js": jsTestDeclRes, ".jsx": jsTestDeclRes, ".mjs": jsTestDeclRes,
	".ts": jsTestDeclRes, ".tsx": jsTestDeclRes,
}
