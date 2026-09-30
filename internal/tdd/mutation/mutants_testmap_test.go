package mutation

import (
	"slices"
	"strings"
	"testing"
)

// The per-function test map: which tests execute which function. The profile
// of one test run alone is turned into the set of functions it executed.

func spansOf(pairs ...any) []funcSpan {
	var spans []funcSpan
	for i := 0; i < len(pairs); i += 3 {
		spans = append(spans, funcSpan{Key: pairs[i].(string), Start: pairs[i+1].(int), End: pairs[i+2].(int)})
	}
	return spans
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestCoveredFuncs_ExecutedBlocksMapToTheirFunctions(t *testing.T) {
	t.Parallel()
	profile := `mode: set
github.com/x/p/a.go:5.10,7.2 1 1
github.com/x/p/a.go:9.10,10.2 1 0
github.com/x/p/a.go:14.3,15.4 2 3
github.com/x/p/b.go:1.1,2.2 1 1
`
	spans := map[string][]funcSpan{
		"a.go": spansOf("f", 4, 8, "g", 9, 11, "h", 13, 16),
		"b.go": spansOf("k", 1, 5),
	}
	got := sortedKeys(coveredFuncs(profile, spans))
	want := []string{"f", "h", "k"}
	if !slices.Equal(got, want) {
		t.Errorf("covered = %v, want %v (g's only block ran zero times)", got, want)
	}
}

// A block's function is the one holding the block's first line, at both ends
// of the function's own span.
func TestCoveredFuncs_SpanEdges(t *testing.T) {
	t.Parallel()
	spans := map[string][]funcSpan{"a.go": spansOf("f", 10, 12)}
	for _, tc := range []struct {
		name  string
		block string
		want  int
	}{
		{"one line above the span", "9.1,9.9", 0},
		{"the first line of the span", "10.1,10.9", 1},
		{"the last line of the span", "12.1,12.9", 1},
		{"one line below the span", "13.1,13.9", 0},
	} {
		profile := "mode: set\nx/a.go:" + tc.block + " 1 1\n"
		if got := len(coveredFuncs(profile, spans)); got != tc.want {
			t.Errorf("%s: %d functions covered, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCoveredFuncs_UnusableInputCoversNothing(t *testing.T) {
	t.Parallel()
	spans := map[string][]funcSpan{"a.go": spansOf("f", 1, 9)}
	for name, profile := range map[string]string{
		"empty":                     "",
		"only the mode line":        "mode: set\n",
		"a line with no position":   "mode: set\nx/a.go 1 1\n",
		"a line with no count":      "mode: set\nx/a.go:2.1,3.2 1\n",
		"a count that is no number": "mode: set\nx/a.go:2.1,3.2 1 many\n",
		"a file with no spans":      "mode: set\nx/other.go:2.1,3.2 1 1\n",
	} {
		if got := coveredFuncs(profile, spans); len(got) != 0 {
			t.Errorf("%s: covered = %v, want none", name, sortedKeys(got))
		}
	}
}

func TestAssembleTestMap_IndexesEachFunctionsTests(t *testing.T) {
	t.Parallel()
	perTest := map[string]map[string]bool{
		"TestB": {"f": true, "g": true},
		"TestA": {"f": true},
		"TestC": {},
	}
	m := assembleTestMap("internal/p", "h1", perTest)
	if m.Package != "internal/p" || m.Hash != "h1" {
		t.Errorf("map = %+v, want package internal/p and hash h1", m)
	}
	if want := []string{"TestA", "TestB", "TestC"}; !slices.Equal(m.Tests, want) {
		t.Errorf("Tests = %v, want %v", m.Tests, want)
	}
	if got, want := m.testsFor("f"), []string{"TestA", "TestB"}; !slices.Equal(got, want) {
		t.Errorf("testsFor(f) = %v, want %v", got, want)
	}
	if got, want := m.testsFor("g"), []string{"TestB"}; !slices.Equal(got, want) {
		t.Errorf("testsFor(g) = %v, want %v", got, want)
	}
	if got := m.testsFor("nothing"); len(got) != 0 {
		t.Errorf("testsFor(nothing) = %v, want none", got)
	}
}

func TestAssembleTestMap_NoTests(t *testing.T) {
	t.Parallel()
	m := assembleTestMap("internal/p", "h", nil)
	if len(m.Tests) != 0 || len(m.Funcs) != 0 {
		t.Errorf("map = %+v, want empty", m)
	}
}

func mapOf(tests []string, funcs map[string][]int) *testMap {
	return &testMap{Schema: testMapSchema, Package: "p", Tests: tests, Funcs: funcs}
}

func TestSelectTests_ByFunction(t *testing.T) {
	t.Parallel()
	m := mapOf([]string{"TestA", "TestB", "TestC"}, map[string][]int{"f": {0, 1}, "g": {2}})
	cases := []struct {
		name      string
		m         *testMap
		current   []string
		touched   []string
		fn        string
		want      []string
		wantWhole bool
	}{
		{"the tests mapped to the function", m, []string{"TestA", "TestB", "TestC"}, nil, "f", []string{"TestA", "TestB"}, false},
		{"one mapped test", m, []string{"TestA", "TestB", "TestC"}, nil, "g", []string{"TestC"}, false},
		{"no map at all", nil, []string{"TestA"}, nil, "f", nil, true},
		{"a function the map does not cover", m, []string{"TestA", "TestB", "TestC"}, nil, "h", nil, true},
		{"a test the map never saw is always selected", m, []string{"TestA", "TestB", "TestC", "TestNew"}, nil, "g",
			[]string{"TestC", "TestNew"}, false},
		{"a test this commit touches is selected", m, []string{"TestA", "TestB", "TestC"}, []string{"TestA"}, "g",
			[]string{"TestA", "TestC"}, false},
		{"a mapped test that no longer exists is dropped", m, []string{"TestA", "TestC"}, nil, "f", []string{"TestA"}, false},
		{"every mapped test gone and none new", m, []string{"TestC"}, nil, "f", nil, true},
		{"a touched test the package no longer has is dropped", m, []string{"TestA", "TestB", "TestC"}, []string{"TestGone"}, "g",
			[]string{"TestC"}, false},
	}
	for _, tc := range cases {
		got, whole := selectTests(tc.m, tc.current, tc.touched, tc.fn)
		if whole != tc.wantWhole || !slices.Equal(got, tc.want) {
			t.Errorf("%s: selectTests = (%v, whole %v), want (%v, whole %v)", tc.name, got, whole, tc.want, tc.wantWhole)
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
		m := mapOf([]string{name}, map[string][]int{"f": {0}})
		got, whole := selectTests(m, []string{name}, nil, "f")
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
