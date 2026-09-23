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

// goTestMainRe matches a Go package's TestMain declaration. It has a test's
// name and no test's meaning: it is the test binary's entry point, `go test
// -run` never selects it, and a proof that names it runs nothing.
var goTestMainRe = regexp.MustCompile(`^\s*func\s+TestMain\s*\(`)

// testDeclLine reports whether an added line declares a test in a file of
// extension ext (lower-cased, with its dot), and whether the table knows the
// language at all. It is the one judgment of "declares a test" every reader
// of testDeclRes goes through, so none of them counts a Go TestMain.
func testDeclLine(ext, line string) (decl, known bool) {
	res, known := testDeclRes[ext]
	if !known {
		return false, false
	}
	if ext == ".go" && goTestMainRe.MatchString(line) {
		return false, true
	}
	for _, re := range res {
		if re.MatchString(line) {
			return true, true
		}
	}
	return false, true
}
