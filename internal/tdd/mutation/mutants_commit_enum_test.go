package mutation

import (
	"fmt"
	"strings"
	"testing"
)

// enumSample is one source the enumerator tests read. The columns below are
// the byte columns gremlins reports for the operator token.
const enumSample = `package p

func f(a, b int) int {
	if a < b {
		return a + b
	}
	a++
	return -a
}
`

func commitLineSet(lines ...int) map[int]bool {
	set := map[int]bool{}
	for _, l := range lines {
		set[l] = true
	}
	return set
}

func namesOf(ms []commitMutant) string {
	var parts []string
	for _, m := range ms {
		parts = append(parts, fmt.Sprintf("%d:%d %s in %s", m.Line, m.Col, m.Mutation, m.Func))
	}
	return strings.Join(parts, "; ")
}

// Each kind of node gremlins mutates by default yields the mutators its
// token maps to, at the operator's own column, in one order.
func TestEnumerateCommitMutants_TokenKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		added map[int]bool
		want  string
	}{
		{"a comparison is both a boundary and a negation", commitLineSet(4),
			"4:7 CONDITIONALS_BOUNDARY in f; 4:7 CONDITIONALS_NEGATION in f"},
		{"an addition is an arithmetic mutant", commitLineSet(5), "5:12 ARITHMETIC_BASE in f"},
		{"an increment is an increment mutant", commitLineSet(7), "7:3 INCREMENT_DECREMENT in f"},
		{"a unary minus is a negation of a negative and an arithmetic one", commitLineSet(8),
			"8:9 ARITHMETIC_BASE in f; 8:9 INVERT_NEGATIVES in f"},
		{"two lines come out in position order", commitLineSet(7, 4),
			"4:7 CONDITIONALS_BOUNDARY in f; 4:7 CONDITIONALS_NEGATION in f; 7:3 INCREMENT_DECREMENT in f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := namesOf(enumerateCommitMutants("p/p.go", []byte(enumSample), tc.added))
			if got != tc.want {
				t.Errorf("mutants = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnumerateCommitMutants_NoLinesNoMutants(t *testing.T) {
	t.Parallel()
	for name, added := range map[string]map[int]bool{
		"nil":                  nil,
		"empty":                {},
		"a line with no token": commitLineSet(1),
		"a line past the end":  commitLineSet(99),
		"a line with an if but no operator of its own": commitLineSet(6),
	} {
		if got := enumerateCommitMutants("p/p.go", []byte(enumSample), added); len(got) != 0 {
			t.Errorf("%s: mutants = %q, want none", name, namesOf(got))
		}
	}
}

func TestEnumerateCommitMutants_SingleLineMutantCarriesItsFileAndKey(t *testing.T) {
	t.Parallel()
	got := enumerateCommitMutants("internal/x/x.go", []byte(enumSample), commitLineSet(5))
	if len(got) != 1 {
		t.Fatalf("mutants = %q, want one", namesOf(got))
	}
	if got[0].File != "internal/x/x.go" || got[0].Func != "f" {
		t.Errorf("mutant = %+v, want file internal/x/x.go and func f", got[0])
	}
}

// Only a position inside a function body has a function to select tests for:
// a package-level initializer on the line just above or just below a function
// yields nothing, and a one-line function is inside itself.
func TestEnumerateCommitMutants_FunctionSpanBoundaries(t *testing.T) {
	t.Parallel()
	src := `package p

var before = 1 + 2
func g(x int) int { return x + 1 }
var after = 3 * 4
`
	got := namesOf(enumerateCommitMutants("p/p.go", []byte(src), commitLineSet(3, 4, 5)))
	if want := "4:30 ARITHMETIC_BASE in g"; got != want {
		t.Errorf("mutants = %q, want %q", got, want)
	}
}

func TestEnumerateCommitMutants_FunctionKeys(t *testing.T) {
	t.Parallel()
	src := `package p

type T struct{}
type Set[K comparable] struct{}

func (t T) val(a int) int { return a + 1 }
func (t *T) ptr(a int) int { return a + 2 }
func (s *Set[K]) has(a int) int { return a + 3 }
func lit() func(int) int {
	return func(a int) int { return a + 4 }
}
`
	got := namesOf(enumerateCommitMutants("p/p.go", []byte(src), commitLineSet(6, 7, 8, 10)))
	want := "6:38 ARITHMETIC_BASE in T.val; 7:39 ARITHMETIC_BASE in T.ptr; " +
		"8:44 ARITHMETIC_BASE in Set.has; 10:36 ARITHMETIC_BASE in lit"
	if got != want {
		t.Errorf("mutants = %q, want %q", got, want)
	}
}

func TestEnumerateCommitMutants_UnparseableSourceHasNoMutants(t *testing.T) {
	t.Parallel()
	if got := enumerateCommitMutants("p/p.go", []byte("package p\nfunc ("), commitLineSet(1, 2)); len(got) != 0 {
		t.Errorf("mutants = %q, want none", namesOf(got))
	}
}
