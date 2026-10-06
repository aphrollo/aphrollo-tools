package mutation

import (
	"slices"
	"strings"
	"testing"
)

// The test map: which tests execute which lines. The profile of one test run
// alone is turned into the blocks it names, and the profiles of all of them
// into one map that answers by line.

func TestCoveredBlocks_ExecutedBlocksAreTrueAndTheRestFalse(t *testing.T) {
	t.Parallel()
	profile := `mode: set
github.com/x/p/a.go:5.10,7.2 1 1
github.com/x/p/a.go:9.10,10.2 1 0
github.com/x/p/a.go:14.3,15.4 2 3
github.com/x/p/b.go:1.1,2.2 1 1
`
	got := coveredBlocks(profile)
	want := map[coverBlock]bool{
		{File: "a.go", From: 5, To: 7}:   true,
		{File: "a.go", From: 9, To: 10}:  false,
		{File: "a.go", From: 14, To: 15}: true,
		{File: "b.go", From: 1, To: 2}:   true,
	}
	if len(got) != len(want) {
		t.Fatalf("blocks = %v, want %v", got, want)
	}
	for b, hit := range want {
		if g, ok := got[b]; !ok || g != hit {
			t.Errorf("block %+v = %v (named %v), want %v: a block that ran zero times is named and not hit", b, g, ok, hit)
		}
	}
}

func TestCoveredBlocks_UnusableInputNamesNothing(t *testing.T) {
	t.Parallel()
	for name, profile := range map[string]string{
		"empty":                     "",
		"only the mode line":        "mode: set\n",
		"a line with no position":   "mode: set\nx/a.go 1 1\n",
		"a line with no count":      "mode: set\nx/a.go:2.1,3.2 1\n",
		"a count that is no number": "mode: set\nx/a.go:2.1,3.2 1 many\n",
	} {
		if got := coveredBlocks(profile); len(got) != 0 {
			t.Errorf("%s: blocks = %v, want none", name, got)
		}
	}
}

func TestAssembleTestMap_ListsEveryBlockWithTheTestsThatRanIt(t *testing.T) {
	t.Parallel()
	a, b, c := coverBlock{File: "p.go", From: 4, To: 4}, coverBlock{File: "p.go", From: 8, To: 9}, coverBlock{File: "p.go", From: 12, To: 12}
	perTest := map[string]map[coverBlock]bool{
		"TestB": {a: true, b: true, c: false},
		"TestA": {a: true, b: false, c: false},
		"TestC": {},
	}
	m := assembleTestMap("internal/p", "h1", perTest)
	if m.Package != "internal/p" || m.Hash != "h1" || m.Schema != testMapSchema {
		t.Errorf("map = %+v, want package internal/p, hash h1 and the current schema", m)
	}
	if want := []string{"TestA", "TestB", "TestC"}; !slices.Equal(m.Tests, want) {
		t.Errorf("Tests = %v, want %v", m.Tests, want)
	}
	for _, tc := range []struct {
		line   int
		want   []string
		listed bool
	}{
		{4, []string{"TestA", "TestB"}, true},
		{8, []string{"TestB"}, true},
		{9, []string{"TestB"}, true},
		{12, nil, true}, // instrumented, and no test ran it
		{10, nil, false},
		{3, nil, false},
	} {
		got, listed := m.testsAt("p.go", tc.line)
		if listed != tc.listed || !slices.Equal(got, tc.want) {
			t.Errorf("testsAt(p.go, %d) = %v listed %v, want %v listed %v", tc.line, got, listed, tc.want, tc.listed)
		}
	}
	if _, listed := m.testsAt("other.go", 4); listed {
		t.Error("a line of a file the map has no block of was listed")
	}
}

func TestAssembleTestMap_NoTests(t *testing.T) {
	t.Parallel()
	m := assembleTestMap("internal/p", "h", nil)
	if len(m.Tests) != 0 || len(m.Blocks) != 0 {
		t.Errorf("map = %+v, want empty", m)
	}
}

// Two blocks that share a line, as one-line `if ok { return }` makes, list the
// tests of both for it.
func TestTestsAt_BlocksSharingALineListTheTestsOfBoth(t *testing.T) {
	t.Parallel()
	m := testMap{Tests: []string{"TestA", "TestB"}, Blocks: []mapBlock{
		{coverBlock{File: "p.go", From: 5, To: 5}, []int{0}},
		{coverBlock{File: "p.go", From: 5, To: 6}, []int{1}},
	}}
	if got, _ := m.testsAt("p.go", 5); !slices.Equal(got, []string{"TestA", "TestB"}) {
		t.Errorf("testsAt(5) = %v, want both", got)
	}
	if got, _ := m.testsAt("p.go", 6); !slices.Equal(got, []string{"TestB"}) {
		t.Errorf("testsAt(6) = %v, want TestB", got)
	}
}

func mapOf(tests []string, blocks ...mapBlock) *testMap {
	return &testMap{Schema: testMapSchema, Package: "p", Tests: tests, Blocks: blocks}
}

func TestSelectTests_ByLine(t *testing.T) {
	t.Parallel()
	m := mapOf([]string{"TestA", "TestB", "TestC"},
		mapBlock{coverBlock{File: "p.go", From: 4, To: 5}, []int{0, 1}},
		mapBlock{coverBlock{File: "p.go", From: 8, To: 8}, []int{2}},
		mapBlock{coverBlock{File: "p.go", From: 12, To: 12}, nil})
	all := []string{"TestA", "TestB", "TestC"}
	cases := []struct {
		name          string
		m             *testMap
		current       []string
		touched       []string
		line          int
		want          []string
		wantWhole     bool
		wantUncovered bool
		wantExact     bool
	}{
		{"the tests that ran the line", m, all, nil, 5, []string{"TestA", "TestB"}, false, false, true},
		{"one test", m, all, nil, 8, []string{"TestC"}, false, false, true},
		{"a line in a block no test ran is uncovered", m, all, nil, 12, nil, false, true, false},
		{"a touched test does not cover a line the exact map says nothing ran", m, all, []string{"TestA"}, 12, nil, false, true, false},
		{"a line in no block runs the tests the commit touched", m, all, []string{"TestB"}, 20, []string{"TestB"}, false, false, false},
		{"a line in no block with nothing touched runs the package", m, all, nil, 20, nil, true, false, false},
		{"no map, a touched test", nil, all, []string{"TestA"}, 5, []string{"TestA"}, false, false, false},
		{"no map, nothing touched", nil, all, nil, 5, nil, true, false, false},
		{"a touched test the package no longer has", nil, all, []string{"TestGone"}, 5, nil, true, false, false},
	}
	for _, tc := range cases {
		got := selectTests(tc.m, tc.current, tc.touched, "p.go", tc.line)
		want := selection{Names: tc.want, Whole: tc.wantWhole, Uncovered: tc.wantUncovered, Exact: tc.wantExact}
		if got.Whole != want.Whole || got.Uncovered != want.Uncovered || got.Exact != want.Exact || !slices.Equal(got.Names, want.Names) {
			t.Errorf("%s: selectTests = %+v, want %+v", tc.name, got, want)
		}
	}
}

// The -run pattern of a selection is bounded so it always fits a command
// line: a pattern of exactly the limit runs, one byte longer runs the whole
// package instead.
func TestSelectTests_PatternLengthBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		patternLn int
		wantWhole bool
	}{
		{"one below the limit", maxRunPatternLen - 1, false},
		{"at the limit", maxRunPatternLen, false},
		{"one above the limit", maxRunPatternLen + 1, true},
	} {
		// "^(" + name + ")$" is the pattern of a single test.
		name := "T" + strings.Repeat("a", tc.patternLn-len("^()$")-1)
		m := mapOf([]string{name}, mapBlock{coverBlock{File: "p.go", From: 3, To: 3}, []int{0}})
		sel := selectTests(m, []string{name}, nil, "p.go", 3)
		got, whole := sel.Names, sel.Whole
		if whole != tc.wantWhole {
			t.Errorf("%s: whole = %v, want %v (pattern %d bytes)", tc.name, whole, tc.wantWhole, len(runPattern([]string{name})))
		}
		if !whole && len(got) != 1 {
			t.Errorf("%s: selected %d tests, want 1", tc.name, len(got))
		}
	}
}

func TestRunPattern_Shapes(t *testing.T) {
	t.Parallel()
	if got, want := runPattern([]string{"TestA"}), "^(TestA)$"; got != want {
		t.Errorf("one test: %q, want %q", got, want)
	}
	if got, want := runPattern([]string{"TestA", "TestB"}), "^(TestA|TestB)$"; got != want {
		t.Errorf("two tests: %q, want %q", got, want)
	}
	if got, want := runPattern([]string{"Test.A"}), `^(Test\.A)$`; got != want {
		t.Errorf("a name with a metacharacter: %q, want %q", got, want)
	}
}

// ratchet: test_removed TestCoveredFuncs_ExecutedBlocksMapToTheirFunctions: the map is keyed by line; the blocks test above is its replacement
// ratchet: test_removed TestCoveredFuncs_SpanEdges: the map is keyed by line, with no function spans to edge
// ratchet: test_removed TestCoveredFuncs_UnusableInputCoversNothing: the map is keyed by line; TestCoveredBlocks_UnusableInputNamesNothing is its replacement
// ratchet: test_removed TestAssembleTestMap_IndexesEachFunctionsTests: the map is keyed by line; TestAssembleTestMap_ListsEveryBlockWithTheTestsThatRanIt is its replacement
// ratchet: test_removed TestSelectTests_ByFunction: the selection is by line; TestSelectTests_ByLine is its replacement
// ratchet: test_removed TestSelectTests_AFunctionTheMapDoesNotListRunsTheTestsThisCommitWrote: a map is exact for the content it was built from, so a test it never saw cannot exist; the unlisted-line cases are in TestSelectTests_ByLine
