package mutation

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// selPlanSet is a set whose index lists line 4 of p/p.go as run by the named
// tests, and lines 8 and 12 as run by none.
func selPlanSet(t *testing.T, root, label string, tags []string, tests ...selTest) selSet {
	t.Helper()
	ran := make([]int, len(tests))
	for i := range tests {
		ran[i] = i
	}
	return selSet{Label: label, Tags: tags, Idx: &selIndex{
		Schema: selSchema, Tests: tests,
		Files:    map[string][]selBlock{"p/p.go": {{From: 4, FromCol: 1, To: 4, ToCol: 80, Tests: ran}, {From: 8, FromCol: 1, To: 8, ToCol: 80}, {From: 12, FromCol: 1, To: 12, ToCol: 80}}},
		FileHash: map[string]string{"p/p.go": selFileHash(root, "p/p.go")},
	}}
}

func selPlanTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "p", "p.go"), "package p\n")
	return root
}

func selRunsOf(stage selStage) map[string][]string {
	out := map[string][]string{}
	for _, r := range stage.Runs {
		out[r.Pkg] = r.Names
	}
	return out
}

func TestPlanSelection_UnitTestsFirstThenOnlyTheTaggedTestsNotAlreadyRun(t *testing.T) {
	root := selPlanTree(t)
	unit := selPlanSet(t, root, "unit", nil, selTest{"a", "TestA"}, selTest{"p", "TestP1"}, selTest{"q", "TestQ"})
	tagged := selPlanSet(t, root, "tags", []string{"integration"}, selTest{"p", "TestInteg"}, selTest{"p", "TestP1"})
	plan := planSelection([]selSet{unit, tagged}, root, "p/p.go", 4, 7)
	if plan.Full != "" || plan.NotCovered || len(plan.Stages) != 2 {
		t.Fatalf("plan = %+v, want two selected stages", plan)
	}
	if plan.Stages[0].Label != "unit" || len(plan.Stages[0].Tags) != 0 {
		t.Errorf("the first stage is %+v, want the unit tests with no tags", plan.Stages[0])
	}
	if got := selRunsOf(plan.Stages[0]); !slices.Equal(got["p"], []string{"TestP1"}) || !slices.Equal(got["q"], []string{"TestQ"}) || !slices.Equal(got["a"], []string{"TestA"}) || len(got) != 3 {
		t.Errorf("unit stage runs %v, want a, p and q", got)
	}
	if plan.Stages[0].Runs[0].Pkg != "p" {
		t.Errorf("the mutated package must run first, got %s", plan.Stages[0].Runs[0].Pkg)
	}
	second := plan.Stages[1]
	if second.Label != "tags" || !slices.Equal(second.Tags, []string{"integration"}) {
		t.Errorf("the second stage is %+v, want the integration tag set", second)
	}
	if got := selRunsOf(second); len(got) != 1 || !slices.Equal(got["p"], []string{"TestInteg"}) {
		t.Errorf("tagged stage runs %v, want only p:[TestInteg] (TestP1 ran in the unit stage)", got)
	}
}

func TestPlanSelection_ALineNoTestExecutesIsNotCoveredAndRunsNothing(t *testing.T) {
	root := selPlanTree(t)
	unit := selPlanSet(t, root, "unit", nil, selTest{"p", "TestP1"})
	tagged := selPlanSet(t, root, "tags", []string{"integration"}, selTest{"p", "TestInteg"})
	plan := planSelection([]selSet{unit, tagged}, root, "p/p.go", 8, 7)
	if !plan.NotCovered || len(plan.Stages) != 0 || plan.Full != "" {
		t.Fatalf("plan = %+v, want not-covered with nothing to run", plan)
	}
}

func TestPlanSelection_EveryDoubtRunsTheFullSuiteAndNamesItsReason(t *testing.T) {
	root := selPlanTree(t)
	good := func() selSet { return selPlanSet(t, root, "unit", nil, selTest{"p", "TestP1"}) }
	withDoubt := func(pkg, why string) selSet {
		s := good()
		s.Idx.Doubt = map[string]string{pkg: why}
		return s
	}
	stale := good()
	stale.Idx.FileHash["p/p.go"] = "an older content"
	cases := map[string]struct {
		sets []selSet
		line int
		want string
	}{
		"no entry":                  {[]selSet{{Label: "unit", Why: selWhyNoEntry}}, 4, selWhyNoEntry},
		"build failure":             {[]selSet{{Label: "unit", Why: selWhyBuild}}, 4, selWhyBuild},
		"a missing second set":      {[]selSet{good(), {Label: "tags", Tags: []string{"x"}, Why: selWhyNoEntry}}, 4, selWhyNoEntry},
		"stale shape":               {[]selSet{stale}, 4, selWhyStale},
		"list failure":              {[]selSet{withDoubt("p", selWhyList)}, 4, selWhyList},
		"test failed alone":         {[]selSet{withDoubt("p", selWhyFailed)}, 4, selWhyFailed},
		"test wrote no profile":     {[]selSet{withDoubt("p", selWhyNoProfile)}, 4, selWhyNoProfile},
		"doubt on a line none runs": {[]selSet{withDoubt("p", selWhyFailed)}, 8, selWhyFailed},
		"line outside every block":  {[]selSet{good()}, 99, selWhyUnlisted},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			plan := planSelection(c.sets, root, "p/p.go", c.line, 7)
			if plan.Full != c.want || plan.NotCovered || len(plan.Stages) != 0 {
				t.Errorf("plan = %+v, want the full suite for %q and nothing selected", plan, c.want)
			}
		})
	}
}

func TestPlanSelection_ADoubtfulImporterRunsWholeAndTheRestStaysSelected(t *testing.T) {
	root := selPlanTree(t)
	s := selPlanSet(t, root, "unit", nil, selTest{"p", "TestP1"})
	s.Idx.Doubt = map[string]string{"q": selWhyFailed}
	s.Idx.PkgTests = map[string]int{"p": 3, "q": 7}
	plan := planSelection([]selSet{s}, root, "p/p.go", 4, 7)
	if plan.Full != "" || len(plan.Stages) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	var whole []string
	for _, r := range plan.Stages[0].Runs {
		if r.Whole {
			whole = append(whole, r.Pkg)
		}
	}
	if !slices.Equal(whole, []string{"q"}) || !slices.Equal(selRunsOf(plan.Stages[0])["p"], []string{"TestP1"}) {
		t.Errorf("runs %+v, want p selected and q whole", plan.Stages[0].Runs)
	}
}

func TestPlanSelection_APlanOutsideTheSourceDirIsStaleNotTrusted(t *testing.T) {
	root := selPlanTree(t)
	s := selPlanSet(t, root, "unit", nil, selTest{"p", "TestP1"})
	if err := os.WriteFile(filepath.Join(root, "p", "p.go"), []byte("package p\n\n// moved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if plan := planSelection([]selSet{s}, root, "p/p.go", 4, 7); plan.Full != selWhyStale {
		t.Errorf("plan = %+v, want stale-shape after the file changed", plan)
	}
}

func TestSelRunExtra_AnAnchoredDeterministicPatternThatNeverMatchesAPrefixSibling(t *testing.T) {
	extra := selRunExtra([]string{"integration"}, selRun{Pkg: "p", Names: []string{"TestB", "TestA", "TestA"}})
	if !slices.Equal(extra, []string{"-tags=integration", "-run", "^(TestA|TestB)$"}) {
		t.Fatalf("extra = %q", extra)
	}
	re := regexp.MustCompile(extra[2])
	for name, want := range map[string]bool{"TestA": true, "TestB": true, "TestAB": false, "XTestA": false, "TestA2": false} {
		if re.MatchString(name) != want {
			t.Errorf("pattern %s matches %q = %v, want %v", extra[2], name, !want, want)
		}
	}
	if got := selRunExtra(nil, selRun{Pkg: "p", Whole: true}); len(got) != 0 {
		t.Errorf("a whole run adds %q, want no -run at all", got)
	}
	long := make([]string, 0, 2000)
	for i := range 2000 {
		long = append(long, "TestAVeryLongTestNameNumber"+string(rune('a'+i%26))+string(rune('a'+i/26%26)))
	}
	if got := selRunExtra(nil, selRun{Pkg: "p", Names: long}); slices.Contains(got, "-run") {
		t.Error("a pattern too long for a command line must run the package whole")
	}
}

func TestPlanSelection_ATestWhoseChildCoverageIsInNoProfileJoinsEverySelectionOfItsPackage(t *testing.T) {
	root := selPlanTree(t)
	s := selPlanSet(t, root, "unit", nil, selTest{"p", "TestP1"})
	s.Idx.Always = map[string][]string{"q": {"TestQSelfStart"}}
	covered := planSelection([]selSet{s}, root, "p/p.go", 4, 7)
	if got := selRunsOf(covered.Stages[0]); !slices.Equal(got["p"], []string{"TestP1"}) || !slices.Equal(got["q"], []string{"TestQSelfStart"}) || len(got) != 2 {
		t.Errorf("a covered line runs %v, want p:[TestP1] and q:[TestQSelfStart]", got)
	}
	// What it executes is unknown, so a line no measured test runs is not
	// called uncovered on the strength of it.
	uncovered := planSelection([]selSet{s}, root, "p/p.go", 8, 7)
	if uncovered.NotCovered || len(uncovered.Stages) != 1 || !slices.Equal(selRunsOf(uncovered.Stages[0])["q"], []string{"TestQSelfStart"}) {
		t.Errorf("an uncovered line = %+v, want q's self-starting test run all the same", uncovered)
	}
}

func TestPlanSelection_APackageWhoseSuiteIsCheapRunsWholeInItsSetAndTheOtherSetStillSelects(t *testing.T) {
	root := selPlanTree(t)
	unit := selSet{Label: "unit", Cheap: map[string]bool{"p": true}}
	tagged := selPlanSet(t, root, "tags", []string{"integration"}, selTest{"p", "TestInteg"})
	plan := planSelection([]selSet{unit, tagged}, root, "p/p.go", 4, 7)
	if plan.Full != "" || len(plan.Stages) != 2 {
		t.Fatalf("plan = %+v, want two stages", plan)
	}
	if first := plan.Stages[0]; first.Label != "unit" || len(first.Runs) != 1 || first.Runs[0].Pkg != "p" || !first.Runs[0].Whole {
		t.Errorf("first stage = %+v, want p whole", first)
	}
	if got := selRunsOf(plan.Stages[1]); !slices.Equal(got["p"], []string{"TestInteg"}) {
		t.Errorf("second stage runs %v, want p:[TestInteg]", got)
	}
}

func TestPlanSelection_WhenEverySetIsCheapTheMutantRunsTheFullSuiteAsItAlwaysDid(t *testing.T) {
	root := selPlanTree(t)
	sets := []selSet{{Label: "unit", Cheap: map[string]bool{"p": true}}, {Label: "tags", Tags: []string{"x"}, Cheap: map[string]bool{"p": true}}}
	if plan := planSelection(sets, root, "p/p.go", 4, 7); plan.Full != selWhyCheap || len(plan.Stages) != 0 {
		t.Errorf("plan = %+v, want the full suite for %s", plan, selWhyCheap)
	}
}
