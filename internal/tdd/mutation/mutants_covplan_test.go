package mutation

import (
	"slices"
	"strings"
	"testing"
)

// covplanScan scans a fixture package of files.
func covplanScan(t *testing.T, files map[string]string) pkgScan {
	t.Helper()
	scan, err := scanPackage(covscanWrite(t, files), nil)
	if err != nil {
		t.Fatal(err)
	}
	return scan
}

// covplanMeasure records that test, run alone, executed exactly the named
// functions, as one block each, and not the others.
func covplanMeasure(st *covStore, scan pkgScan, test string, funcs ...string) {
	blocks := map[coverBlock]bool{}
	for _, f := range scan.Funcs {
		if !f.Test {
			blocks[coverBlock{File: f.File, From: f.Start, To: f.End}] = slices.Contains(funcs, f.Key)
		}
	}
	for _, v := range scan.Vars {
		if !v.Test {
			blocks[coverBlock{File: v.File, From: v.Start, To: v.End}] = slices.Contains(funcs, v.Key)
		}
	}
	recordProfile(st, scan, test, blocks)
}

// covplanFull is a store holding every test of the standard fixture, measured.
func covplanFull(scan pkgScan) *covStore {
	st := &covStore{Schema: covSchema}
	planCoverage(st, scan, nil)
	covplanMeasure(st, scan, "Test_Direct", "leaf")
	covplanMeasure(st, scan, "Test_Helper", "helper", "leaf")
	covplanMeasure(st, scan, "Test_MethodValue", "T.Method", "helper", "leaf")
	covplanMeasure(st, scan, "Test_Unrelated", "Unrelated")
	covplanMeasure(st, scan, "Test_ViaTable", "var:viaTable", "var:table")
	covplanMeasure(st, scan, "Test_Reflect")
	return st
}

func covplanFiles(lib string) map[string]string {
	return map[string]string{"lib.go": lib, "lib_test.go": covscanTests}
}

func TestPlanCoverage_ColdMeasuresOnlyTheStaticCandidates(t *testing.T) {
	scan := covplanScan(t, covplanFiles(covscanLib))
	st := &covStore{Schema: covSchema}
	plan := planCoverage(st, scan, []string{"leaf"})
	if want := []string{"Test_Direct", "Test_Helper", "Test_MethodValue"}; !slices.Equal(plan.Measure, want) {
		t.Fatalf("measure = %v, want %v", plan.Measure, want)
	}
	if len(plan.Valid) != 0 || len(plan.Stale) != 6 {
		t.Fatalf("valid %v stale %v", plan.Valid, plan.Stale)
	}
}

func TestPlanCoverage_UnchangedTreeNeedsNothing(t *testing.T) {
	scan := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(scan)
	plan := planCoverage(st, scan, []string{"leaf"})
	if len(plan.Stale) != 0 || len(plan.Measure) != 0 || len(plan.Valid) != 6 || plan.Reset {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanCoverage_MovedFunctionKeepsEntriesAndRemapsLines(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	moved := covplanScan(t, covplanFiles("package p\n\n// one\n// more\n// lines\n"+covscanLib[len("package p\n"):]))
	plan := planCoverage(st, moved, []string{"leaf"})
	if len(plan.Stale) != 0 {
		t.Fatalf("a moved function must keep its entries, stale = %v", plan.Stale)
	}
	m := st.view(moved, plan.Valid, nil, 0)
	line := moved.Funcs["leaf"].Start
	got, listed := m.testsAt("lib.go", line)
	if !listed || !slices.Equal(got, []string{"Test_Direct", "Test_Helper", "Test_MethodValue"}) {
		t.Fatalf("tests at the new line %d = %v (listed %v)", line, got, listed)
	}
	if old, _ := m.testsAt("lib.go", before.Funcs["leaf"].Start); len(old) != 0 && before.Funcs["leaf"].Start != line {
		if moved.funcAt("lib.go", before.Funcs["leaf"].Start) == "leaf" {
			t.Fatalf("the old line must not map to leaf any more")
		}
	}
}

func TestPlanCoverage_EditedFunctionInvalidatesOnlyTheTestsThatCoveredIt(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	edited := strings.Replace(covscanLib, "return leaf() }", "return leaf() + 1 }", 1)
	after := covplanScan(t, covplanFiles(edited))
	plan := planCoverage(st, after, []string{"helper"})
	wantStale := []string{"Test_Helper", "Test_MethodValue"}
	slices.Sort(plan.Stale)
	if !slices.Equal(plan.Stale, wantStale) {
		t.Fatalf("stale = %v, want %v", plan.Stale, wantStale)
	}
	if !slices.Equal(plan.Measure, wantStale) {
		t.Fatalf("measure = %v, want %v", plan.Measure, wantStale)
	}
	if len(plan.Valid) != 4 || plan.Reset {
		t.Fatalf("valid = %v reset = %v", plan.Valid, plan.Reset)
	}
}

func TestPlanCoverage_EditedFunctionRemeasuresTestsThatCoveredItEvenWhenNotCandidates(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	edited := strings.Replace(covscanLib, "func Unrelated() int { return 2 }", "func Unrelated() int { return 3 }", 1)
	after := covplanScan(t, covplanFiles(edited))
	plan := planCoverage(st, after, []string{"leaf"})
	if !slices.Contains(plan.Measure, "Test_Unrelated") {
		t.Fatalf("a test that covered the edited function is measured again, measure = %v", plan.Measure)
	}
}

func TestPlanCoverage_AddedTestIsStaleAndMeasuredWhenACandidate(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	files := covplanFiles(covscanLib)
	files["lib_test.go"] += "\nfunc Test_New(t *testing.T) { _ = leaf() }\n"
	after := covplanScan(t, files)
	plan := planCoverage(st, after, []string{"leaf"})
	if !slices.Equal(plan.Stale, []string{"Test_New"}) || !slices.Equal(plan.Measure, []string{"Test_New"}) {
		t.Fatalf("stale %v measure %v", plan.Stale, plan.Measure)
	}
}

func TestPlanCoverage_EditedTestIsStale(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	files := covplanFiles(covscanLib)
	files["lib_test.go"] = strings.Replace(covscanTests, "func Test_Direct(t *testing.T) { _ = leaf() }", "func Test_Direct(t *testing.T) { _ = leaf() + 1 }", 1)
	after := covplanScan(t, files)
	plan := planCoverage(st, after, []string{"leaf"})
	if !slices.Equal(plan.Stale, []string{"Test_Direct"}) {
		t.Fatalf("stale = %v", plan.Stale)
	}
}

func TestPlanCoverage_RenamedFileKeepsEntries(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	after := covplanScan(t, map[string]string{"renamed.go": covscanLib, "lib_test.go": covscanTests})
	plan := planCoverage(st, after, []string{"leaf"})
	if len(plan.Stale) != 0 {
		t.Fatalf("a file rename must keep the entries, stale = %v", plan.Stale)
	}
	m := st.view(after, plan.Valid, nil, 0)
	if got, listed := m.testsAt("renamed.go", after.Funcs["leaf"].Start); !listed || len(got) != 3 {
		t.Fatalf("tests at renamed.go = %v (listed %v)", got, listed)
	}
}

func TestPlanCoverage_NonFunctionDeclarationResetsTheStore(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	after := covplanScan(t, covplanFiles(covscanLib+"\nvar extra = 1\n"))
	plan := planCoverage(st, after, []string{"leaf"})
	if !plan.Reset || len(plan.Valid) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if want := []string{"Test_Direct", "Test_Helper", "Test_MethodValue"}; !slices.Equal(plan.Measure, want) {
		t.Fatalf("after a reset only the candidates are measured, got %v", plan.Measure)
	}
}

func TestPlanCoverage_EditedTestMainResetsTheStore(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	files := covplanFiles(covscanLib)
	files["lib_test.go"] = strings.Replace(covscanTests, "m.Run() }", "m.Run(); _ = 1 }", 1)
	plan := planCoverage(st, covplanScan(t, files), nil)
	if !plan.Reset {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanCoverage_EditedTestHelperInvalidatesTheTestsThatReachIt(t *testing.T) {
	files := covplanFiles(covscanLib)
	files["lib_test.go"] = covscanTests + "\nfunc fixture() int { return 1 }\n"
	files["lib_test.go"] = strings.Replace(files["lib_test.go"], "func Test_Direct(t *testing.T) { _ = leaf() }", "func Test_Direct(t *testing.T) { _ = leaf() + fixture() }", 1)
	before := covplanScan(t, files)
	st := covplanFull(before)
	files["lib_test.go"] = strings.Replace(files["lib_test.go"], "func fixture() int { return 1 }", "func fixture() int { return 2 }", 1)
	plan := planCoverage(st, covplanScan(t, files), nil)
	if !slices.Equal(plan.Stale, []string{"Test_Direct"}) {
		t.Fatalf("stale = %v", plan.Stale)
	}
}

func TestPlanCoverage_DeletedTestIsDropped(t *testing.T) {
	before := covplanScan(t, covplanFiles(covscanLib))
	st := covplanFull(before)
	files := covplanFiles(covscanLib)
	files["lib_test.go"] = strings.Replace(covscanTests, "func Test_Reflect(t *testing.T)   { _ = \"leaf\" }\n", "", 1)
	plan := planCoverage(st, covplanScan(t, files), nil)
	if len(plan.Valid) != 5 || len(plan.Stale) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if _, kept := st.Tests["Test_Reflect"]; kept {
		t.Fatal("the entry of a deleted test must go")
	}
}

func TestStoreView_ListsBlocksNoTestRanAndMarksPartial(t *testing.T) {
	scan := covplanScan(t, covplanFiles(covscanLib))
	st := &covStore{Schema: covSchema}
	planCoverage(st, scan, nil)
	covplanMeasure(st, scan, "Test_Direct", "leaf")
	m := st.view(scan, []string{"Test_Direct"}, nil, 5)
	if !m.Partial {
		t.Fatal("five unmeasured tests make the map partial")
	}
	if got, listed := m.testsAt("lib.go", scan.Funcs["Unrelated"].Start); !listed || len(got) != 0 {
		t.Fatalf("Unrelated is a listed block no measured test ran: %v %v", got, listed)
	}
	if complete := st.view(scan, []string{"Test_Direct"}, nil, 0); complete.Partial {
		t.Fatal("no unmeasured test, no partial map")
	}
}
