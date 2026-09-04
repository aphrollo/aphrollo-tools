package tdd

import (
	"path"
	"path/filepath"
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
	// Zig: a .zig file usually holds BOTH production code and `test "..." {}`
	// blocks inline, so at file level it is Source unless it is an explicit
	// test file (see isTestFile). `.zon` (build.zig.zon manifest) is not code,
	// so it stays out of this set and classifies as Ignore.
	".zig": true,
	// RON is Rust code's data half: embedded item registries, the
	// locomotion key tables, frozen schema fixtures. Editing one changes
	// program behaviour, so it is Source (never Test -- a .ron declares no
	// test, whatever directory it sits in) and its owning package resolves
	// through the nearest ancestor Cargo.toml, exactly as a .rs does.
	".ron": true,
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
	if base == gateConfigName {
		// Not prose: this file carries the keys the gate reads to decide what
		// a lane owes, and the suite pins them through the readers. Left as
		// Ignore, a commit that touched nothing else took the docs-only fast
		// path -- no suite, by design -- and changed the gate's own behaviour
		// without running the test that pins it (issue #212).
		return Source
	}
	ext := strings.ToLower(path.Ext(base))
	if ext == ".ron" && !ronHasOwningCrate(p) {
		// A .ron nobody's [package] owns (borld's assets/**) resolves to an
		// EMPTY package, and an empty -p scope runs the WHOLE workspace —
		// the heaviest run there is, from editing an asset.
		return Ignore
	}
	if sourceExts[ext] {
		return Source
	}
	if goEmbedsFile(p) {
		return Source
	}
	return Ignore
}

// ronHasOwningCrate reports whether a real [package] manifest covers p.
func ronHasOwningCrate(p string) bool {
	dir := filepath.Dir(filepath.FromSlash(p))
	for {
		if cargoPackageName(filepath.Join(dir, "Cargo.toml")) != "" {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
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
		// Cargo's integration-test convention puts the role in the directory:
		// every .rs under a `tests/` segment is a test target (or a module of
		// one), whatever its basename. Same shape as the Zig `tests/` rule
		// below; gated to .rs here because testDirs is JS/TS-only.
		if strings.HasSuffix(base, "_test.rs") || strings.HasPrefix(base, "test_") {
			return true
		}
		for seg := range strings.SplitSeq(path.Dir(p), "/") {
			if seg == "tests" {
				return true
			}
		}
		return false
	case ".zig":
		// Zig tests are `test "..." {}` blocks INLINE in ordinary src/*.zig
		// files, so a .zig file is normally BOTH source and test. We do NOT
		// flip such files to Test: that would run the oracle smell gate over
		// production code and false-block legitimate calls (e.g. a real
		// std.time.sleep in a src file). Inline-test coverage is instead
		// reached block-scoped via test-block extraction in the smells gate,
		// not by file kind. So only an EXPLICIT test file counts here: a
		// `*_test.zig` suffix, or any .zig under a `tests/` directory segment
		// (e.g. tests/integration_test.zig). This `tests/` rule is gated to
		// .zig here rather than added to testDirs, which is JS/TS-only.
		if strings.HasSuffix(base, "_test.zig") {
			return true
		}
		for seg := range strings.SplitSeq(path.Dir(p), "/") {
			if seg == "tests" {
				return true
			}
		}
		return false
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

// gateConfigName is the per-repo config the gate reads its own policy from.
const gateConfigName = "aphrollo.toml"
