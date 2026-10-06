package mutation

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The per-function test map answers "which tests execute this function" for
// one package, so a mutant in that function is run against those tests and
// not the whole package. It is built by compiling the package's test binary
// once and running each test alone under a coverage profile
// (mutants_testmap_build.go), never on the commit path, and it is keyed by
// function because a commit edits function bodies and shifts every line, not
// the names.

// testMapSchema is the shape of the map on disk. A map of another schema is
// read as no map.
const testMapSchema = 1

// maxRunPatternLen bounds the `-run` pattern of one selection, so it always
// fits a command line (Windows allows 32,767 characters for the whole line).
// A selection past it runs the whole package instead.
const maxRunPatternLen = 8000

// testMap is one package's map: Funcs holds, per function key, the indexes
// into Tests of the tests that executed it.
type testMap struct {
	Schema  int              `json:"schema"`
	Package string           `json:"package"`
	Hash    string           `json:"hash"`
	Tests   []string         `json:"tests"`
	Funcs   map[string][]int `json:"funcs"`
}

// testsFor is the tests the map lists for a function, nil when it lists none.
func (m testMap) testsFor(fn string) []string {
	var names []string
	for _, i := range m.Funcs[fn] {
		names = append(names, m.Tests[i])
	}
	return names
}

// coveredFuncs is the set of function keys one test's cover profile shows
// executed. A block belongs to the function holding its first line, so a
// function literal counts for the declaration around it. spans is per file
// base name; a line that is not a block, or one with a zero count, is
// skipped.
func coveredFuncs(profile string, spans map[string][]funcSpan) map[string]bool {
	covered := map[string]bool{}
	for line := range strings.SplitSeq(profile, "\n") {
		// A block line is exactly `file:l.c,l.c statements count`; the
		// `mode: set` header has two fields and is skipped with the rest.
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil || count < 1 {
			continue
		}
		file, first, ok := blockStart(fields[0])
		if !ok {
			continue
		}
		if fn := enclosingFunc(spans[file], first); fn != "" {
			covered[fn] = true
		}
	}
	return covered
}

// blockRe reads a profile block's position, `file.go:12.3,14.2`, into the
// file and the block's first line.
var blockRe = regexp.MustCompile(`^(.+):(\d+)\.\d+,\d+\.\d+$`)

// blockStart answers the base name of a profile block's file and its first
// line.
func blockStart(pos string) (file string, line int, ok bool) {
	m := blockRe.FindStringSubmatch(pos)
	if m == nil {
		return "", 0, false
	}
	line, err := strconv.Atoi(m[2])
	return path.Base(m[1]), line, err == nil
}

// assembleTestMap folds each test's covered functions into one map, its
// tests and each function's list of them in name order.
func assembleTestMap(pkg, hash string, perTest map[string]map[string]bool) testMap {
	names := make([]string, 0, len(perTest))
	for name := range perTest {
		names = append(names, name)
	}
	slices.Sort(names)
	m := testMap{Schema: testMapSchema, Package: pkg, Hash: hash, Tests: names, Funcs: map[string][]int{}}
	for i, name := range names {
		for fn := range perTest[name] {
			m.Funcs[fn] = append(m.Funcs[fn], i)
		}
	}
	for _, idx := range m.Funcs {
		slices.Sort(idx)
	}
	return m
}

// selectTests is the tests a mutant in fn is run against: what the map lists
// for it, plus every test the map has never seen and every test this commit
// touches, since the map cannot say what those execute — keeping only the
// tests the package still has. A function the map does not list, or no map at
// all, is run against those added and touched tests alone: they are what the
// commit wrote for it, and a mutant they miss goes on to the rest of the
// package. whole is true when no selection can be made, and the whole package
// runs instead: nothing added or touched to run, nothing left of the listed
// tests, or a pattern too long for a command line.
func selectTests(m *testMap, current, touched []string, fn string) (names []string, whole bool) {
	var mapped, mapTests []string
	if m != nil {
		mapped, mapTests = m.testsFor(fn), m.Tests
	}
	known := make(map[string]bool, len(mapTests))
	for _, name := range mapTests {
		known[name] = true
	}
	want := map[string]bool{}
	for _, name := range mapped {
		want[name] = true
	}
	for _, name := range current {
		if m != nil && !known[name] {
			want[name] = true
		}
	}
	for _, name := range touched {
		want[name] = true
	}
	for _, name := range current {
		if want[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(names) == 0 || len(runPattern(names)) > maxRunPatternLen {
		return nil, true
	}
	return names, false
}

// runPattern is the `-run` pattern that selects exactly the named tests.
func runPattern(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}
