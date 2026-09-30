package mutation

import (
	"slices"
	"strings"
	"testing"
)

// Only Go production code is mutated: not a test, not test data, not vendored
// code, and not a file of another language.
// The measurement's own file list leaves out what the commit-time run leaves
// out: test data and a ratchet law's fixtures are data for a test, never code
// of the repo, whatever their extension says.
func TestMeasureGoDiff_LeavesOutTestDataAndRatchetFixtures(t *testing.T) {
	root := makeGoRepo(t)
	base := strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD"))
	for _, rel := range []string{
		"pkg/real.go",
		"pkg/real_test.go",
		"pkg/testdata/data.go",
		".ratchet/fixtures/law/hit/fixture.go",
		".ratchet/fixturesx/kept.go",
		"pkg/testdatax/kept.go",
	} {
		write(t, root, rel, "package p\n")
	}
	gitDo(t, root, "add", "-A")

	got, err := measureGoDiff(root, base)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{".ratchet/fixturesx/kept.go", "pkg/real.go", "pkg/testdatax/kept.go"}
	if !slices.Equal(got, want) {
		t.Errorf("measureGoDiff = %v, want %v", got, want)
	}
}

func TestIsCommitSource_WhatIsMutated(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file string
		want bool
	}{
		{"internal/x/x.go", true},
		{"x.go", true},
		{"internal/x/x_test.go", false},
		{"x_test.go", false},
		{"internal/x/testdata/x.go", false},
		{"testdata/x.go", false},
		{"internal/testdatax/x.go", true},
		{".ratchet/fixtures/law/hit/x.go", false},
		{"sub/.ratchet/fixtures/x.go", false},
		{".ratchet/fixturesx/x.go", true},
		{".ratchet/laws/x.go", true},
		{"vendor/m/x.go", false},
		{"internal/vendor/m/x.go", false},
		{"internal/vendored/x.go", true},
		{"internal/x/x.rs", false},
		{"internal/x/go", false},
		{"", false},
	} {
		if got := isCommitSource(tc.file); got != tc.want {
			t.Errorf("isCommitSource(%q) = %v, want %v", tc.file, got, tc.want)
		}
	}
}

func TestCommitMutantsOf_SelectsSourcesAndNamesWhatItLeavesOut(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "gate/gate.go", commitGateSource)
	write(t, root, "gate/other.go", commitGateSource)
	write(t, root, "gate/gate_test.go", commitGateSource)
	write(t, root, "gate/testdata/x.go", commitGateSource)
	write(t, root, "gate/tagged.go", "//go:build neverbuilt\n\n"+commitGateSource)
	added := map[string]map[int]bool{
		"gate/gate.go":       commitLineSet(4),
		"gate/other.go":      commitLineSet(4),
		"gate/gate_test.go":  commitLineSet(4),
		"gate/testdata/x.go": commitLineSet(4),
		"gate/deleted.go":    commitLineSet(4),
		"gate/tagged.go":     commitLineSet(6),
	}
	mutants, notes := commitMutantsOf(root, added, map[string]bool{"gate/other.go": true})
	var got []string
	for _, m := range mutants {
		got = append(got, m.File+":"+m.Mutation)
	}
	want := []string{"gate/gate.go:CONDITIONALS_BOUNDARY", "gate/gate.go:CONDITIONALS_NEGATION"}
	if !slices.Equal(got, want) {
		t.Errorf("mutants = %v, want %v (a test, test data, a missing file, another platform's file and a file with unstaged edits are left out)", got, want)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "gate/other.go") || !strings.Contains(notes[0], "unstaged") {
		t.Errorf("notes = %v, want one naming gate/other.go and its unstaged edits", notes)
	}
}

func TestCommitMutantsOf_NothingStaged(t *testing.T) {
	mutants, notes := commitMutantsOf(t.TempDir(), nil, nil)
	if len(mutants) != 0 || len(notes) != 0 {
		t.Errorf("mutants %v notes %v, want none", mutants, notes)
	}
}

func TestUnstagedFiles_ReadsGitsNameList(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n")
	write(t, root, "b.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "two files")
	write(t, root, "a.go", "package m\n\n// edited\n")
	got, err := unstagedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got["a.go"] {
		t.Errorf("unstaged = %v, want only a.go", got)
	}
}

func TestStagedAddedLines_OnlyTheStagedChange(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n\nfunc f() {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "a")
	write(t, root, "a.go", "package m\n\nfunc f() {}\n\nfunc g() {}\n")
	write(t, root, "b.go", "package m\n")
	gitDo(t, root, "add", "a.go")
	got, err := stagedAddedLines(root)
	if err != nil {
		t.Fatal(err)
	}
	if !got["a.go"][5] || got["b.go"] != nil {
		t.Errorf("added = %v, want a.go line 5 and nothing for the unstaged b.go", got)
	}
}

func TestCommitPlans_ReadsTheTestsEachPackageHas(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "gate/gate.go", commitGateSource)
	write(t, root, "gate/gate_test.go",
		"package gate\n\nimport \"testing\"\n\nfunc TestKind_A(t *testing.T) {}\n\nfunc TestKind_B(t *testing.T) {}\n")
	kept := testMap{Schema: testMapSchema, Package: "gate", Hash: "h", Tests: []string{"TestKind_A"}, Funcs: map[string][]int{"Kind": {0}}}
	if err := saveTestMap(root, kept); err != nil {
		t.Fatal(err)
	}
	added := map[string]map[int]bool{"gate/gate_test.go": commitLineSet(7)}
	plans := commitPlans(root, []commitMutant{kindMutant, labelMutant}, added)
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want one per package", len(plans))
	}
	p := plans["gate"]
	if p.Map == nil || p.Map.Hash != "h" {
		t.Errorf("map = %+v, want the kept one", p.Map)
	}
	if want := []string{"TestKind_A", "TestKind_B"}; !slices.Equal(p.Current, want) {
		t.Errorf("Current = %v, want %v", p.Current, want)
	}
	if want := []string{"TestKind_B"}; !slices.Equal(p.Touched, want) {
		t.Errorf("Touched = %v, want %v", p.Touched, want)
	}
	if p.Whole {
		t.Error("Whole is set with no TestMain change")
	}
}

func TestCommitPlans_NoKeptMapIsANilMap(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "gate/gate.go", commitGateSource)
	plans := commitPlans(root, []commitMutant{kindMutant}, nil)
	if p := plans["gate"]; p == nil || p.Map != nil || len(p.Current) != 0 {
		t.Errorf("plan = %+v, want a plan with no map and no tests", p)
	}
	if plans := commitPlans(root, nil, nil); len(plans) != 0 {
		t.Errorf("plans = %v for no mutants, want none", plans)
	}
}

func TestGapSummary_CountsEachKind(t *testing.T) {
	t.Parallel()
	if got := gapSummary(nil); got != "" {
		t.Errorf("no gaps = %q, want empty", got)
	}
	if got := gapSummary(map[string]int{gapBudget: 1}); got != "1 budget" {
		t.Errorf("one gap = %q, want %q", got, "1 budget")
	}
	if got, want := gapSummary(map[string]int{gapRunner: 2, gapBudget: 3, gapUnconfirmed: 1}), "3 budget, 2 runner, 1 unconfirmed"; got != want {
		t.Errorf("three kinds = %q, want %q", got, want)
	}
}

func TestCommitReport_SeparatesMeasuredFromNot(t *testing.T) {
	t.Parallel()
	measured := commitRun{Mutant: kindMutant, Outcome: MutantOutcome{File: "gate/gate.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_BOUNDARY", Status: "caught"}}
	survivor := commitRun{Mutant: kindMutant, Outcome: MutantOutcome{File: "gate/gate.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_NEGATION", Status: "missed"}}
	gap := commitRun{Mutant: labelMutant, NotMeasured: "cut", GapKind: gapBudget}
	v, n, gaps := commitReport(MutantsConfig{}, []commitRun{measured, survivor, gap})
	if n != 2 || v.Tested != 2 || v.Caught != 1 || len(v.Unaccepted) != 1 || !v.Refused {
		t.Errorf("verdict = %+v over %d measured, want 2 tested, 1 caught and a refused survivor", v, n)
	}
	if gaps != "1 budget" {
		t.Errorf("gaps = %q, want %q", gaps, "1 budget")
	}
	v, n, gaps = commitReport(MutantsConfig{}, []commitRun{gap})
	if n != 0 || v.Refused || gaps != "1 budget" {
		t.Errorf("all unmeasured: verdict %+v n %d gaps %q, want nothing measured and nothing refused", v, n, gaps)
	}
	if _, n, gaps = commitReport(MutantsConfig{}, nil); n != 0 || gaps != "" {
		t.Errorf("no runs: n %d gaps %q, want nothing", n, gaps)
	}
}
