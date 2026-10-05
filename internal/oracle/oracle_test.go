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

// detectLines is Detect over a code view, failing on an unknown detector.
func detectLines(t *testing.T, detector, code string) []int {
	t.Helper()
	lines, ok := Detect(detector, Input{Code: code, Whole: code})
	if !ok {
		t.Fatalf("%s: unknown detector", detector)
	}
	return lines
}

func TestDetect_EachTautologyFormNamesItsOwnLineAndOnlyWhenTheOperandsMatch(t *testing.T) {
	cases := []struct {
		name string
		code string
		want []int
	}{
		{"self compare", "a\nif x == x {\n}\nif x == y {\n}\n", []int{2}},
		{"jest expect", "a\nexpect(v).toBe(v)\nexpect(v).toBe(w)\nexpect().toBe()\n", []int{2}},
		{"node equal", "a\nassert.strictEqual(v, v)\nassert.equal(v, w)\n", []int{2}},
		{"zig expectEqual", "a\ntry expectEqual(v, v);\ntry expectEqual(v, w);\n", []int{2}},
		{"literal true", "a\nassert!(true)\nassert!(done)\nassert True\nassert Done\n", []int{2, 4}},
		{"two on one line", "a\nx == x; y == y\nb\n", []int{2}},
		{"first line", "x == x\n", []int{1}},
	}
	for _, c := range cases {
		if got := detectLines(t, Tautology, c.code); !slices.Equal(got, c.want) {
			t.Errorf("%s: lines %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDetect_AMemberAccessIsNotAFocusedOrDisabledAlias(t *testing.T) {
	// Every character that makes `fit(` part of a longer name or a member call
	// hides it; the characters either side of each class's edge do not.
	for _, c := range []string{".", "_", "a", "z", "A", "Z", "0", "9"} {
		for _, d := range []string{FocusedTest, DisabledTest} {
			code := "x" + c + "fit(1)\nx" + c + "xit(1)\n"
			if got := detectLines(t, d, code); len(got) != 0 {
				t.Errorf("%s: %q read as an alias, lines %v", d, c, got)
			}
		}
	}
	for _, c := range []string{"`", "{", "@", "[", "/", ":", " ", "(", "-"} {
		if got := detectLines(t, FocusedTest, "a\n"+c+"fit(1)\n"); !slices.Equal(got, []int{2}) {
			t.Errorf("focused after %q: lines %v, want [2]", c, got)
		}
		if got := detectLines(t, DisabledTest, "a\n"+c+"xit(1)\n"); !slices.Equal(got, []int{2}) {
			t.Errorf("disabled after %q: lines %v, want [2]", c, got)
		}
	}
	if got := detectLines(t, FocusedTest, "fit(1)\n"); !slices.Equal(got, []int{1}) {
		t.Errorf("fit( at the start of the text: lines %v, want [1]", got)
	}
	if got := detectLines(t, DisabledTest, "xit(1)\n"); !slices.Equal(got, []int{1}) {
		t.Errorf("xit( at the start of the text: lines %v, want [1]", got)
	}
}

func TestDetect_ASpecificErrorWithinTwoLinesIsASafetyNet(t *testing.T) {
	blind, safe := "require.Error(t, err)", "require.ErrorIs(t, err, e)"
	cases := []struct {
		name string
		code string
		want []int
	}{
		{"two below", blind + "\nx\n" + safe + "\n", nil},
		{"three below", blind + "\nx\nx\n" + safe + "\n", []int{1}},
		{"two above", safe + "\nx\n" + blind + "\n", nil},
		{"three above", safe + "\nx\nx\n" + blind + "\n", []int{4}},
		{"same line", blind + "; " + safe + "\n", nil},
		{"last line, alone", "x\n" + blind, []int{2}},
	}
	for _, c := range cases {
		if got := detectLines(t, ErrorKindBlind, c.code); !slices.Equal(got, c.want) {
			t.Errorf("%s: lines %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDetect_APanicOnlyOracleNeedsARecoverAndADiscardAndNoAssertionOutside(t *testing.T) {
	head := "func TestA_x(t *testing.T) {\n"
	deferRecover := "\tdefer func() {\n\t\tif r := recover(); r != nil {\n\t\t\tt.Fatalf(\"%v\", r)\n\t\t}\n\t}()\n"
	cases := []struct {
		name string
		code string
		want []int
	}{
		{"the shape", "package a\n\n" + head + deferRecover + "\t_ = f(1)\n}\n", []int{3}},
		{"the shape running to the end of the text", head + deferRecover + "\t_ = f(1)\n", []int{1}},
		{"discard before the defer", head + "\t_ = f(1)\n" + deferRecover + "}\n", []int{1}},
		{"nothing discarded", head + deferRecover + "\tf(1)\n}\n", nil},
		{"an assertion outside the catcher", head + deferRecover + "\t_ = f(1)\n\trequire.Equal(t, 1, 1)\n}\n", nil},
		{"a defer that does not recover", head + "\tdefer func() {\n\t\tt.Fatalf(\"x\")\n\t}()\n\t_ = f(1)\n}\n", nil},
		{"no defer", head + "\t_ = f(1)\n}\n", nil},
		{"the next test is not this one's body", head + deferRecover + "}\n\nfunc TestA_y(t *testing.T) {\n\t_ = f(1)\n}\n", nil},
	}
	for _, c := range cases {
		if got := detectLines(t, PanicOnlyOracle, c.code); !slices.Equal(got, c.want) {
			t.Errorf("%s: lines %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDetect_ATimerArmIsBoundedOnlyByASelectThatCanBeSeen(t *testing.T) {
	whole := "select {\ncase got = <-done:\ncase <-time.After(d):\n}\n"
	arm := "case <-time.After(d):\n"
	if got, _ := Detect(TestSleep, Input{Code: arm, Whole: whole}); len(got) != 0 {
		t.Errorf("an arm whose select has two arms in the whole file was read as a sleep: %v", got)
	}
	if got, _ := Detect(TestSleep, Input{Code: arm}); !slices.Equal(got, []int{1}) {
		t.Errorf("with no whole file the arm's select cannot be seen: lines %v, want [1]", got)
	}
	// One bare copy of the same arm text makes the edit a sleep.
	both := whole + "select {\ncase <-time.After(d):\n}\n"
	if got, _ := Detect(TestSleep, Input{Code: arm, Whole: both}); !slices.Equal(got, []int{1}) {
		t.Errorf("an arm that also stands alone in a one-armed select: lines %v, want [1]", got)
	}
	// A nested select inside an arm adds no arm to the outer one.
	nested := "select {\ncase <-time.After(d):\n\tswitch {\n\tcase a:\n\tcase b:\n\t}\n}\n"
	if got, _ := Detect(TestSleep, Input{Code: arm, Whole: nested}); !slices.Equal(got, []int{1}) {
		t.Errorf("a switch inside the arm counted as arms of the select: lines %v, want [1]", got)
	}
}

func TestHas_IsWhetherDetectFindsALine(t *testing.T) {
	if !Has(TestSleep, Input{Code: "time.Sleep(1)\n"}) || Has(TestSleep, Input{Code: "x\n"}) {
		t.Fatal("Has disagrees with Detect")
	}
	if Has("no-such-smell", Input{Code: "time.Sleep(1)\n"}) {
		t.Fatal("an unknown detector found something")
	}
}

func TestReadsDirectives_IsTheSuppressionDetectorsAlone(t *testing.T) {
	for _, name := range Names() {
		want := name == LintSuppress || name == TypeSuppress || name == CoverageSuppress
		if got := ReadsDirectives(name); got != want {
			t.Errorf("ReadsDirectives(%s) = %v, want %v", name, got, want)
		}
	}
}
