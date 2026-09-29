package suite

import (
	"reflect"
	"testing"
)

// These are suite's own tests of vacuous_cargo.go's cargoTargetNamer and
// cargoVacuousTargets, reached today only through internal/tdd/precommit's
// gate tests.

const (
	cargoZeroFiltered = "test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 109 filtered out; finished in 0.00s\n"
	cargoZeroNone     = "test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	cargoSomePassed   = "test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 4 filtered out; finished in 0.10s\n"
)

// TestCargoTargetNamer_NamesTheNearestHeaderBeforeThePosition pins the lookup:
// a position is attributed to the last Running header before it, not the first
// and not one after it.
func TestCargoTargetNamer_NamesTheNearestHeaderBeforeThePosition(t *testing.T) {
	t.Parallel()
	out := "     Running unittests src/lib.rs (target/debug/deps/alpha-1)\nbody a\n" +
		"     Running tests/it.rs (target/debug/deps/it-2)\nbody b\n"
	namer := cargoTargetNamer(out)
	posB := len("     Running unittests src/lib.rs (target/debug/deps/alpha-1)\nbody a\n") + 5
	if got := namer(posB); got != "tests/it.rs" {
		t.Fatalf("name at second block = %q, want tests/it.rs", got)
	}
	if got := namer(len("     Running unittests src/lib.rs (target/debug/deps/alpha-1)\n") + 2); got != "src/lib.rs" {
		t.Fatalf("name at first block = %q, want src/lib.rs", got)
	}
}

// TestCargoTargetNamer_AHeaderAtThePositionIsNotBeforeIt pins the boundary: a
// header that STARTS at the queried offset does not precede it, so it does not
// name a block that ends up printed above it.
func TestCargoTargetNamer_AHeaderAtThePositionIsNotBeforeIt(t *testing.T) {
	t.Parallel()
	out := "     Running unittests src/lib.rs (x)\n"
	if got := cargoTargetNamer(out)(0); got != "unknown target" {
		t.Fatalf("name at the header's own offset = %q, want unknown target", got)
	}
}

// TestCargoTargetNamer_NoHeaderIsAnUnknownTarget pins the fallback.
func TestCargoTargetNamer_NoHeaderIsAnUnknownTarget(t *testing.T) {
	t.Parallel()
	if got := cargoTargetNamer("no headers here\n")(10); got != "unknown target" {
		t.Fatalf("name = %q, want unknown target", got)
	}
}

// TestCargoTargetNamer_DocTestsHeadersNameTheirBlocksAndSortWithRunning pins
// that the two header shapes share one position-ordered list: a Doc-tests
// header printed after a Running one names what follows it.
func TestCargoTargetNamer_DocTestsHeadersNameTheirBlocksAndSortWithRunning(t *testing.T) {
	t.Parallel()
	out := "     Running unittests src/lib.rs (x)\nbody\n   Doc-tests widget\nresult\n"
	namer := cargoTargetNamer(out)
	if got := namer(len(out) - 2); got != "widget" {
		t.Fatalf("name after the Doc-tests header = %q, want widget", got)
	}
}

// TestCargoVacuousTargets_NamesATargetThatLostEveryTestToAFilter pins the
// verdict: zero passed, zero failed, filtered out > 0, attributed to the header
// above it.
func TestCargoVacuousTargets_NamesATargetThatLostEveryTestToAFilter(t *testing.T) {
	t.Parallel()
	out := "     Running unittests src/lib.rs (x)\n" + cargoZeroFiltered
	if got, want := cargoVacuousTargets(out), []string{"src/lib.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cargoVacuousTargets = %v, want %v", got, want)
	}
}

// TestCargoVacuousTargets_ATargetWithNoTestsAtAllIsNotVacuous pins the other
// half of libtest's ground truth: zero everything with nothing filtered is a
// target that never had tests, not one that lost them.
func TestCargoVacuousTargets_ATargetWithNoTestsAtAllIsNotVacuous(t *testing.T) {
	t.Parallel()
	out := "     Running unittests src/lib.rs (x)\n" + cargoZeroNone
	if got := cargoVacuousTargets(out); len(got) != 0 {
		t.Fatalf("cargoVacuousTargets = %v, want none", got)
	}
}

// TestCargoVacuousTargets_APassedTestMakesTheTargetReal pins that any passed
// test clears it, filtered out or not.
func TestCargoVacuousTargets_APassedTestMakesTheTargetReal(t *testing.T) {
	t.Parallel()
	out := "     Running unittests src/lib.rs (x)\n" + cargoSomePassed
	if got := cargoVacuousTargets(out); len(got) != 0 {
		t.Fatalf("cargoVacuousTargets = %v, want none", got)
	}
}

// TestCargoVacuousTargets_AFailedTestMakesTheTargetReal pins that a failed
// test also clears it: the target ran something.
func TestCargoVacuousTargets_AFailedTestMakesTheTargetReal(t *testing.T) {
	t.Parallel()
	out := "     Running unittests src/lib.rs (x)\n" +
		"test result: FAILED. 0 passed; 2 failed; 0 ignored; 0 measured; 5 filtered out; finished in 0.00s\n"
	if got := cargoVacuousTargets(out); len(got) != 0 {
		t.Fatalf("cargoVacuousTargets = %v, want none", got)
	}
}

// TestCargoVacuousTargets_ASummaryWithNoHeaderIsAnUnknownTarget pins the
// fallback name when nothing precedes the block.
func TestCargoVacuousTargets_ASummaryWithNoHeaderIsAnUnknownTarget(t *testing.T) {
	t.Parallel()
	if got, want := cargoVacuousTargets(cargoZeroFiltered), []string{"unknown target"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cargoVacuousTargets = %v, want %v", got, want)
	}
}

// TestCargoVacuousTargets_ListsEachTargetOnceSorted pins the output shape:
// deduplicated (a target printing two vacuous summaries is named once) and
// sorted, whatever order they ran in.
func TestCargoVacuousTargets_ListsEachTargetOnceSorted(t *testing.T) {
	t.Parallel()
	out := "     Running tests/zz.rs (x)\n" + cargoZeroFiltered +
		"     Running tests/aa.rs (x)\n" + cargoZeroFiltered + cargoZeroFiltered +
		"   Doc-tests mid\n" + cargoZeroFiltered
	want := []string{"mid", "tests/aa.rs", "tests/zz.rs"}
	if got := cargoVacuousTargets(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("cargoVacuousTargets = %v, want %v", got, want)
	}
}

// TestCargoVacuousTargets_NoOutputIsNoTargets pins the empty stream.
func TestCargoVacuousTargets_NoOutputIsNoTargets(t *testing.T) {
	t.Parallel()
	if got := cargoVacuousTargets(""); len(got) != 0 {
		t.Fatalf("cargoVacuousTargets = %v, want none", got)
	}
}
