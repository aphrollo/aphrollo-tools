package oracle

import (
	"slices"
	"testing"
)

func TestDetect_NamesTheLinesAnOracleSmellSitsOn(t *testing.T) {
	cases := []struct {
		detector string
		code     string
		want     []int
	}{
		{TestSleep, "a\ntime.Sleep(1)\nb\nasyncio.sleep(2)\n", []int{2, 4}},
		{TestSleep, "select {\ncase got = <-done:\ncase <-time.After(d):\n}\n", nil},
		{TestSleep, "select {\ncase <-time.After(d):\n}\n", []int{2}},
		{Tautology, "x\nassert x == x\nassert.True(t, true)\ny\n", []int{2, 3}},
		{Tautology, "assert x == y\nexpect(f()).toBe(f())\n", nil},
		{FocusedTest, "a\nit.only('x', f)\nobj.fit(1)\nfit(2)\n", []int{2, 4}},
		{DisabledTest, "a\nxit('x')\nt.Skip()\nobj.xit(1)\n", []int{2, 3}},
		{ErrorKindBlind, "ok\nrequire.Error(t, err)\nx\nx\nx\nrequire.ErrorIs(t, err, e)\n", []int{2}},
		{PanicOnlyOracle, "package a\n\nfunc TestA_x(t *testing.T) {\n\tdefer func() {\n\t\tif r := recover(); r != nil {\n\t\t\tt.Fatalf(\"%v\", r)\n\t\t}\n\t}()\n\t_ = f(1)\n}\n", []int{3}},
	}
	for _, c := range cases {
		got, ok := Detect(c.detector, Input{Code: c.code, Whole: c.code})
		if !ok {
			t.Fatalf("%s: unknown detector", c.detector)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s over %q: lines %v, want %v", c.detector, c.code, got, c.want)
		}
	}
}

func TestDetect_ReadsSuppressionFromTheDirectivesView(t *testing.T) {
	in := Input{Directives: "a\nx := 1 //nolint\nb\n// @ts-ignore\n# type: ignore\n"}
	for detector, want := range map[string][]int{LintSuppress: {2}, TypeSuppress: {4, 5}, CoverageSuppress: nil} {
		got, ok := Detect(detector, in)
		if !ok || !slices.Equal(got, want) {
			t.Errorf("%s: %v (known %v), want %v", detector, got, ok, want)
		}
	}
}

func TestDetect_AnUnknownDetectorIsNotKnown(t *testing.T) {
	if _, ok := Detect("no-such-smell", Input{}); ok {
		t.Fatal("Detect knew a detector nobody declared")
	}
}

func TestNames_ListsEveryDetectorSorted(t *testing.T) {
	want := []string{"coverage-suppress", "disabled-test", "error-kind-blind", "focused-test", "lint-suppress", "panic-only-oracle", "tautology", "test-sleep", "type-suppress"}
	if got := Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestInput_AViewNeedsOnlyTheTextItReads(t *testing.T) {
	// A code-view detector never looks at Directives, nor a directives-view one
	// at Code: a caller fills the view its detector reads.
	if lines, _ := Detect(TestSleep, Input{Directives: "time.Sleep(1)\n"}); len(lines) != 0 {
		t.Fatalf("test-sleep read the directives view: %v", lines)
	}
	if lines, _ := Detect(LintSuppress, Input{Code: "//nolint\n"}); len(lines) != 0 {
		t.Fatalf("lint-suppress read the code view: %v", lines)
	}
}
