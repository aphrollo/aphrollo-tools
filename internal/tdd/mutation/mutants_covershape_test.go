package mutation

import (
	"os"
	"path/filepath"
	"testing"
)

// Issue #910's trap (a). Go coverage never counts some positions however
// often they execute, and gremlins files every mutant there NOT COVERED:
// #939's `vacuous_dispatch.go:22` was a bare case condition a hand proof
// killed. A not-covered mutant on an added line is refused, so the gate must
// know those positions apart from code no test runs.
//
// testdata/covershape/shapes.go.txt is a module whose one test executes
// every function in it (each branch, each case, each closure), and
// gremlins.json is gremlins v0.6.0's own report over it. Every NOT COVERED
// there is therefore a position coverage cannot count, and every mutant it
// ran is one coverage did count. The static model must agree with the tool
// on all of them.
func TestCoverShapeAt_AgreesWithGremlinsOnAFullyExercisedModule(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("testdata", "covershape", "shapes.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join("testdata", "covershape", "gremlins.json"))
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := parseGremlinsReport(report)
	if err != nil {
		t.Fatal(err)
	}
	// Lines 5 and 7 are the package-level var initializers and line 163 is
	// in the body of a function named `_`; every other NOT COVERED is inside
	// an instrumented function body.
	packageLevel := map[int]bool{5: true, 7: true, 163: true}
	if len(outcomes) != 75 {
		t.Fatalf("read %d mutants from the fixture report, want the 75 gremlins listed", len(outcomes))
	}
	for _, m := range outcomes {
		want := shapeCoverable
		switch {
		case m.Status == gremlinsNotCovered && packageLevel[m.Line]:
			want = shapeOutsideFunc
		case m.Status == gremlinsNotCovered:
			want = shapeCoverGap
		}
		if got := coverShapeAt(src, m.Line, m.Col); got != want {
			t.Errorf("%d:%d %s (%s): shape %v, want %v", m.Line, m.Col, m.Mutation, m.Status, got, want)
		}
	}
}

// A function Go's cover tool refuses to instrument at all — the blank name
// cannot be called — is as uncountable as a package-level line.
func TestCoverShapeAt_ABlankNamedFunctionIsOutsideAnyCountedBody(t *testing.T) {
	t.Parallel()
	src := []byte("package p\n\nfunc _() int {\n\treturn 1 + 2\n}\n\nfunc f() int {\n\treturn 1 + 2\n}\n")
	if got := coverShapeAt(src, 4, 11); got != shapeOutsideFunc {
		t.Errorf("blank-named body: shape %v, want %v", got, shapeOutsideFunc)
	}
	if got := coverShapeAt(src, 8, 11); got != shapeCoverable {
		t.Errorf("named body: shape %v, want %v", got, shapeCoverable)
	}
}

// ARITHMETIC_BASE on a string concatenation does not compile, whichever `+`
// of the chain it lands on: no test can ever run it.
func TestStringConcatAt_FindsEveryPlusOfAChainCarryingAStringLiteral(t *testing.T) {
	t.Parallel()
	src := []byte("package p\n\nfunc f(a, b string, n int) (string, int) {\n\treturn a + b + \"!\", (n + 1) * 2\n}\n")
	for _, c := range []struct {
		col  int
		want bool
	}{
		{11, true},  // a + b, inside a chain ending in a literal
		{15, true},  // … + "!"
		{25, false}, // n + 1
	} {
		if got := stringConcatAt(src, 4, c.col); got != c.want {
			t.Errorf("col %d: stringConcatAt = %v, want %v", c.col, got, c.want)
		}
	}
}

// Where the model cannot find the mutated operator it claims nothing: the
// mutant is coverable, so it stays refusable, and it is no concatenation.
func TestCoverShapeAt_APositionWithNoOperatorClaimsNothing(t *testing.T) {
	t.Parallel()
	src := []byte("package p\n\nvar x = \"a\" + \"b\"\n")
	if got := coverShapeAt([]byte("this is not Go"), 1, 1); got != shapeCoverable {
		t.Errorf("unparseable source: shape %v, want %v", got, shapeCoverable)
	}
	if got := coverShapeAt(src, 3, 7); got != shapeCoverable {
		t.Errorf("no operator at 3:7: shape %v, want %v", got, shapeCoverable)
	}
	if stringConcatAt(src, 3, 7) || stringConcatAt([]byte("this is not Go"), 1, 1) {
		t.Error("stringConcatAt claimed a concatenation where no operator starts")
	}
}

// Parentheses carry a chain: the literal outside them still makes the
// inner `+` a concatenation.
func TestStringConcatAt_FollowsTheChainThroughParentheses(t *testing.T) {
	t.Parallel()
	src := []byte("package p\n\nfunc f(a, b string) string {\n\treturn \"x\" + (a + b)\n}\n")
	if !stringConcatAt(src, 4, 18) {
		t.Error("the + inside (a + b) was not read as part of a concatenation with \"x\"")
	}
}
