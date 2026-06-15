package tdd

import (
	"path"
	"strings"
)

// Kind is how the TDD gates classify a file path. The gates treat test and
// source files differently (smells gate tests; source edits flow) and ignore
// everything else, so misclassification is a direct correctness bug: a test
// read as source escapes the smell gate, and a source read as test gets gated
// wrongly. The original `classifyFile` missed several real test patterns
// (`_test.ts`, bare `spec.js`, JS `conftest`) and never excluded vendored
// code, so those are the corrections baked in here.
type Kind int

const (
	// Ignore is anything the gates must not act on: non-code files and, just
	// as importantly, vendored/generated trees that the project does not own.
	Ignore Kind = iota
	Source
	Test
)

func (k Kind) String() string {
	switch k {
	case Source:
		return "source"
	case Test:
		return "test"
	default:
		return "ignore"
	}
}

// ignoredDirs are path segments whose contents the project does not author.
// Editing a `*.test.ts` inside node_modules must never trip a TDD gate.
// Go's `testdata` is fixtures, not test code — the go tool ignores it, so do
// we. `.git` guards against tooling that surfaces objects as paths.
var ignoredDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"testdata":     true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".git":         true,
}

// testDirs are segments that mark a directory of tests in JS/TS layouts, where
// the test role lives in the directory rather than the filename.
var testDirs = map[string]bool{
	"__tests__": true,
	"__test__":  true,
}

// sourceExts are the code extensions a non-test file must have to be Source.
// A file outside this set (Markdown, JSON, YAML, lockfiles, …) is Ignore, so
// editing a README never engages the gates.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".rs": true, ".java": true, ".rb": true,
	".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".cjs": true, ".mjs": true, ".cts": true, ".mts": true,
}

// ClassifyFile maps a file path to the role the TDD gates should treat it as.
func ClassifyFile(p string) Kind {
	p = strings.ReplaceAll(p, "\\", "/")
	base := path.Base(p)

	for seg := range strings.SplitSeq(path.Dir(p), "/") {
		if ignoredDirs[seg] {
			return Ignore
		}
	}

	if isTestFile(p, base) {
		return Test
	}
	if sourceExts[strings.ToLower(path.Ext(base))] {
		return Source
	}
	return Ignore
}

// isTestFile recognises a test by filename convention across the languages the
// gates support, plus JS/TS test directories.
func isTestFile(p, base string) bool {
	ext := strings.ToLower(path.Ext(base))
	stem := base[:len(base)-len(path.Ext(base))]

	switch ext {
	case ".go":
		return strings.HasSuffix(base, "_test.go")
	case ".py":
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || base == "conftest.py"
	case ".rs":
		return strings.HasSuffix(base, "_test.rs") || strings.HasPrefix(base, "test_")
	case ".js", ".jsx", ".ts", ".tsx", ".cjs", ".mjs", ".cts", ".mts":
		// `.test.`/`.spec.` infix, `_test` suffix, or a bare spec/conftest
		// file — every form the original classifier missed.
		if strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") ||
			strings.HasSuffix(stem, "_test") || strings.HasSuffix(stem, "_spec") ||
			stem == "spec" || stem == "conftest" {
			return true
		}
	}

	for seg := range strings.SplitSeq(path.Dir(p), "/") {
		if testDirs[seg] && sourceExts[ext] {
			return true
		}
	}
	return false
}
