package mutation

import (
	"cmp"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The test map answers "which tests execute this line" for one package, so a
// mutant on that line is run against those tests and not the whole package,
// and a mutant on a line no test executes is named not covered and not run at
// all. It is built by compiling the package's test binary once and running
// each test alone under a coverage profile (mutants_testmap_build.go) at the
// commit that needs it, in the foreground, and kept keyed by the package's
// content (mutants_testmap_store.go), so the next commit to the same content
// reuses it. A map is exact for the content it was built from: its lines are
// that content's lines.

// testMapSchema is the shape of the map on disk. A map of another schema is
// read as no map.
const testMapSchema = 2

// maxRunPatternLen bounds the `-run` pattern of one selection, so it always
// fits a command line (Windows allows 32,767 characters for the whole line).
// A selection past it runs the whole package instead.
const maxRunPatternLen = 8000

// coverBlock is one block of statements a coverage profile names: the file's
// base name and the first and last line of the block.
type coverBlock struct {
	File string `json:"f"`
	From int    `json:"a"`
	To   int    `json:"b"`
}

// mapBlock is a block and the indexes into Tests of the tests that executed
// it; none says the block was instrumented and no test ran it.
type mapBlock struct {
	coverBlock
	Tests []int `json:"t,omitempty"`
}

// testMap is one package's map: Blocks lists every block of the package's own
// statements, with the tests that executed it.
type testMap struct {
	Schema  int        `json:"schema"`
	Package string     `json:"package"`
	Hash    string     `json:"hash"`
	Tests   []string   `json:"tests"`
	Blocks  []mapBlock `json:"blocks"`
	// Unknown lists tests that wrote no profile, so nothing is known of what
	// they execute. A map with any is used for this commit only, never kept,
	// and never exact: those tests are added to every selection and a mutant
	// is never called not covered on its word.
	Unknown []string `json:"unknown,omitempty"`
	// Partial says some test of the package has no measurement in this map: it
	// was not reached in the time, or an edit took its entry and it was not
	// remeasured. A line it lists may be executed by a test the map does not
	// name, so a mutant the named tests miss is not called a survivor.
	Partial bool `json:"-"`
	// Unmeasured is how many tests have no measurement (the reason Partial is
	// set).
	Unmeasured int `json:"-"`
	// Inexact says some test was measured under other dependencies or fixtures
	// than the tree has now: the tests it names are a good first run, but they
	// are not all that could execute the line, so no selection is exact.
	Inexact bool `json:"-"`
}

// testsAt is the tests that executed a block holding the line of the named
// file, in name order, and whether any block holds it at all. A line in no
// block, such as a declaration outside a function, is not one the profile can
// speak for; a line in blocks no test ran is listed with no tests.
func (m testMap) testsAt(file string, line int) (names []string, listed bool) {
	seen := map[int]bool{}
	for _, b := range m.Blocks {
		if b.File != file || line < b.From || line > b.To {
			continue
		}
		listed = true
		for _, i := range b.Tests {
			seen[i] = true
		}
	}
	for i := range seen {
		names = append(names, m.Tests[i])
	}
	slices.Sort(names)
	return names, listed
}

// coveredBlocks is every block one test's cover profile names, true for the
// ones it executed. A line that is not a block, such as the `mode: set`
// header, is skipped.
func coveredBlocks(profile string) map[coverBlock]bool {
	blocks := map[coverBlock]bool{}
	for line := range strings.SplitSeq(profile, "\n") {
		// A block line is exactly `file:l.c,l.c statements count`.
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		b, ok := parseBlock(fields[0])
		if !ok {
			continue
		}
		blocks[b] = blocks[b] || count > 0
	}
	return blocks
}

// blockRe reads a profile block's position, `file.go:12.3,14.2`.
var blockRe = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+$`)

// parseBlock answers the base name of a profile block's file and its lines.
func parseBlock(pos string) (coverBlock, bool) {
	m := blockRe.FindStringSubmatch(pos)
	if m == nil {
		return coverBlock{}, false
	}
	from, err1 := strconv.Atoi(m[2])
	to, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil {
		return coverBlock{}, false
	}
	return coverBlock{File: path.Base(m[1]), From: from, To: to}, true
}

// assembleTestMap folds each test's blocks into one map: its tests in name
// order, and every block any profile named with the tests that ran it.
func assembleTestMap(pkg, hash string, perTest map[string]map[coverBlock]bool) testMap {
	names := make([]string, 0, len(perTest))
	for name := range perTest {
		names = append(names, name)
	}
	slices.Sort(names)
	ran := map[coverBlock][]int{}
	for i, name := range names {
		for b, hit := range perTest[name] {
			if _, ok := ran[b]; !ok {
				ran[b] = nil
			}
			if hit {
				ran[b] = append(ran[b], i)
			}
		}
	}
	m := testMap{Schema: testMapSchema, Package: pkg, Hash: hash, Tests: names, Blocks: make([]mapBlock, 0, len(ran))}
	for b, idx := range ran {
		m.Blocks = append(m.Blocks, mapBlock{coverBlock: b, Tests: idx})
	}
	slices.SortFunc(m.Blocks, func(x, y mapBlock) int {
		return cmp.Or(strings.Compare(x.File, y.File), cmp.Compare(x.From, y.From), cmp.Compare(x.To, y.To))
	})
	return m
}

// selection is what selectTests decides for one mutant: the tests to run
// first, or that the whole package runs, or that no test could kill it.
// Exact says the tests are every test that executes the line, so a mutant
// they miss is a survivor and no further run could change that.
type selection struct {
	Names     []string
	Whole     bool
	Uncovered bool
	Exact     bool
	// Partial says the map the selection came from is partial: the tests are
	// the ones measured, and a mutant they miss is not a survivor (NOT
	// MEASURED), nor is a line none of them ran uncovered.
	Partial bool
}

// selectTests is the tests a mutant on a line is run against. With a map and
// a line it lists, they are the tests that ran it: none is uncovered, and the
// mutant is not run. Without a map, or on a line no block holds, the tests
// this commit touched are the cheap first run, the ones it wrote for the code,
// and a mutant they miss goes on to the rest of the package. Whole is set when
// no selection can be made and the whole package runs instead: nothing touched
// to run, or a pattern too long for a command line.
func selectTests(m *testMap, current, touched []string, file string, line int) selection {
	if m != nil {
		if mapped, listed := m.testsAt(file, line); listed {
			if m.Partial {
				names := slices.Concat(mapped, m.Unknown)
				slices.Sort(names)
				names = slices.Compact(names)
				if len(runPattern(names)) > maxRunPatternLen {
					return selection{Whole: true}
				}
				return selection{Names: names, Partial: true}
			}
			if m.Inexact || len(m.Unknown) > 0 {
				// What the tests with no profile execute is not known, and what an
				// inexact map's tests read has changed: the unknown tests join
				// the selection, and a mutant they all miss still goes on to the
				// rest of the package.
				names := slices.Concat(mapped, m.Unknown)
				slices.Sort(names)
				names = slices.Compact(names)
				if len(names) == 0 || len(runPattern(names)) > maxRunPatternLen {
					return selection{Whole: true}
				}
				return selection{Names: names}
			}
			// The map is exact for the content it was built from and its tests
			// are the ones the test binary listed, so each of them exists.
			switch {
			case len(mapped) == 0:
				return selection{Uncovered: true}
			case len(runPattern(mapped)) > maxRunPatternLen:
				return selection{Whole: true}
			}
			return selection{Names: mapped, Exact: true}
		}
	}
	want := map[string]bool{}
	for _, name := range touched {
		want[name] = true
	}
	var names []string
	for _, name := range current {
		if want[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(names) == 0 || len(runPattern(names)) > maxRunPatternLen {
		return selection{Whole: true}
	}
	return selection{Names: names}
}

// runPattern is the `-run` pattern that selects exactly the named tests.
func runPattern(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}
