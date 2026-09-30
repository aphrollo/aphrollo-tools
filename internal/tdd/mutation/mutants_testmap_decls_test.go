package mutation

import (
	"path/filepath"
	"slices"
	"testing"
)

// declFixture writes a package directory whose test files hold every kind of
// declaration the scan tells apart, and answers the decls read from it.
func declFixture(t *testing.T) []testDecl {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.go"), `package p

import "testing"

func helper() int { return 1 }

var table = []int{1, 2}

// Test_One checks one.
func Test_One(t *testing.T) {
	t.Log(helper())
}

func Test_Two(t *testing.T) {}

func ExampleThing() {}

func FuzzIt(f *testing.F) {}

func BenchmarkX(b *testing.B) {}

func TestMain(m *testing.M) { m.Run() }
`)
	mustWrite(t, filepath.Join(dir, "b_test.go"), "package p_test\n\nimport \"testing\"\n\nfunc Test_Three(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(dir, "x.go"), "package p\n\nfunc Test_NotATest() {}\n")
	return scanTestDecls(dir, "internal/p")
}

func TestScanTestDecls_NamesTheTestsTheRunnerRuns(t *testing.T) {
	t.Parallel()
	want := []string{"ExampleThing", "FuzzIt", "Test_One", "Test_Three", "Test_Two"}
	if got := testNames(declFixture(t)); !slices.Equal(got, want) {
		t.Errorf("testNames = %v, want %v", got, want)
	}
}

func TestScanTestDecls_NoTestFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "x.go"), "package p\n")
	if got := scanTestDecls(dir, "internal/p"); len(got) != 0 {
		t.Errorf("decls = %v, want none", got)
	}
	if got := scanTestDecls(filepath.Join(dir, "missing"), "internal/p"); len(got) != 0 {
		t.Errorf("decls of a missing dir = %v, want none", got)
	}
}

// The runner's own rule for a function it runs: the prefix, then anything but
// a lowercase letter, and the parameter the prefix promises.
func TestRunnerTestKind_NamePrefixes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		want declKind
	}{
		{"Test", kindTest},
		{"Test_x", kindTest},
		{"TestA", kindTest},
		{"Testlower", kindHelper},
		{"Fuzz", kindTest},
		{"FuzzA", kindTest},
		{"Fuzzy", kindHelper},
		{"Example", kindTest},
		{"ExampleA", kindTest},
		{"Examples", kindHelper},
		{"TestMain", kindMain},
		{"Benchmark", kindHelper},
		{"", kindHelper},
	} {
		if got := runnerTestKind(tc.name); got != tc.want {
			t.Errorf("runnerTestKind(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func addedIn(file string, lines ...int) map[string]map[int]bool {
	return map[string]map[int]bool{file: commitLineSet(lines...)}
}

// Lines added inside a test mark that test; the first and last line of the
// function are inside it, and the blank line after or the doc line before are
// not.
func TestTestChanges_LinesInATestMarkThatTest(t *testing.T) {
	t.Parallel()
	decls := declFixture(t)
	const file = "internal/p/a_test.go"
	for _, tc := range []struct {
		name  string
		lines []int
		want  []string
	}{
		{"the middle of the test", []int{11}, []string{"Test_One"}},
		{"the func line of the test", []int{10}, []string{"Test_One"}},
		{"the closing line of the test", []int{12}, []string{"Test_One"}},
		{"the doc comment above the test", []int{9}, nil},
		{"the blank line below the test", []int{13}, nil},
		{"two tests at once", []int{11, 14}, []string{"Test_One", "Test_Two"}},
		{"a one-line test", []int{16}, []string{"ExampleThing"}},
	} {
		touched, whole := testChanges(decls, addedIn(file, tc.lines...))
		if whole || !slices.Equal(touched, tc.want) {
			t.Errorf("%s: touched %v whole %v, want %v whole false", tc.name, touched, whole, tc.want)
		}
	}
}

// A helper, a package variable or TestMain changed in a test file means the
// tests cannot be told apart by what they touch.
func TestTestChanges_HelpersAndTestMain(t *testing.T) {
	t.Parallel()
	decls := declFixture(t)
	const file = "internal/p/a_test.go"
	allOfA := []string{"ExampleThing", "FuzzIt", "Test_One", "Test_Two"}
	touched, whole := testChanges(decls, addedIn(file, 5))
	if whole || !slices.Equal(touched, allOfA) {
		t.Errorf("a helper func: touched %v whole %v, want %v", touched, whole, allOfA)
	}
	touched, whole = testChanges(decls, addedIn(file, 7))
	if whole || !slices.Equal(touched, allOfA) {
		t.Errorf("a package var: touched %v whole %v, want %v", touched, whole, allOfA)
	}
	if _, whole = testChanges(decls, addedIn(file, 22)); !whole {
		t.Errorf("TestMain changed: whole = false, want true")
	}
}

func TestTestChanges_NothingRelevant(t *testing.T) {
	t.Parallel()
	decls := declFixture(t)
	for name, added := range map[string]map[string]map[int]bool{
		"nil":                        nil,
		"a source file, not a test":  addedIn("internal/p/x.go", 3),
		"the imports of a test file": addedIn("internal/p/a_test.go", 3),
		"another package":            addedIn("internal/q/a_test.go", 11),
	} {
		touched, whole := testChanges(decls, added)
		if whole || len(touched) != 0 {
			t.Errorf("%s: touched %v whole %v, want nothing", name, touched, whole)
		}
	}
}
