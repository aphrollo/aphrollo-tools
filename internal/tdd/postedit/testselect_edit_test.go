package postedit

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const selectOld = "package p\n\nvar limit = 3\n\nfunc F1() int {\n\treturn 1\n}\n\nfunc F2() int { return limit }\n"

const selectTestsOld = "package p\n\nimport \"testing\"\n\nfunc helper() int { return 1 }\n\nfunc TestOne(t *testing.T) { _ = F1() }\n\nfunc TestTwo(t *testing.T) { _ = F2() }\n"

// selectProject is a Go module with package internal/p, the test-select key
// as given ("" writes none), and file rel (under internal/p) holding edited.
func selectProject(t *testing.T, key, rel, edited string) (root, target string) {
	t.Helper()
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root = mkProject(t, "go.mod")
	if key != "" {
		mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\ntest-select = \""+key+"\"\n")
	}
	target = filepath.Join(root, "internal", "p", rel)
	mustWrite(t, target, edited)
	return root, target
}

// selectSeams answers the HEAD text of the edited file with old and the query
// with q, and records each query.
func selectSeams(t *testing.T, old string, q CoverQuery) *[][]string {
	t.Helper()
	var calls [][]string
	prevOld, prevQuery := testSelectOldSource, testSelectQuery
	t.Cleanup(func() { testSelectOldSource, testSelectQuery = prevOld, prevQuery })
	testSelectOldSource = func(root, rel string) []byte { return []byte(old) }
	testSelectQuery = func(root, dir string, funcs []string) CoverQuery {
		calls = append(calls, slices.Concat([]string{dir}, funcs))
		return q
	}
	return &calls
}

// editAndSee is the run PostEdit makes for the edit, and the line it answers.
func editAndSee(t *testing.T, target string) (seen Runner, line string) {
	t.Helper()
	line = PostEdit(postPayload("Edit", target), func(r Runner, _ string) SuiteResult {
		seen = r
		return SuiteResult{Passed: true, Output: "=== RUN   TestOne\n--- PASS: TestOne (0.00s)\nPASS\nok  \texample.com/p\t0.01s\n"}
	})
	return seen, line
}

func TestPostEdit_TestSelectOffLeavesTheArgvAsItWas(t *testing.T) {
	for _, key := range []string{"", "off"} {
		_, target := selectProject(t, key, "p.go", strings.Replace(selectOld, "return 1", "return 11", 1))
		asked := selectSeams(t, selectOld, CoverQuery{Fresh: true, Tests: []string{"TestOne"}, Total: 3})
		seen, line := editAndSee(t, target)
		if want := []string{"test", "./internal/p"}; !slices.Equal(seen.Args, want) || seen.Select != nil {
			t.Errorf("test-select %q: args = %v, select = %+v, want %v and none", key, seen.Args, seen.Select, want)
		}
		if len(*asked) != 0 || strings.Contains(line, "selected") || strings.Contains(line, "full suite") {
			t.Errorf("test-select %q: queried %v, line = %q; want neither", key, *asked, line)
		}
	}
}

func TestPostEdit_TestSelectRunsTheCoveringTestsOfAnEditedFunction(t *testing.T) {
	root, target := selectProject(t, "edit", "p.go", strings.Replace(selectOld, "return 1", "return 11", 1))
	asked := selectSeams(t, selectOld, CoverQuery{Fresh: true, Tests: []string{"TestOne"}, Total: 3})
	seen, line := editAndSee(t, target)
	if want := []string{"test", "./internal/p", "-run=^(TestOne)$"}; !slices.Equal(seen.Args, want) {
		t.Fatalf("args = %q, want %q", seen.Args, want)
	}
	if want := [][]string{{"internal/p", "F1"}}; !slices.EqualFunc(*asked, want, slices.Equal) {
		t.Fatalf("queries = %v, want %v", *asked, want)
	}
	if !strings.Contains(line, "selected 1 of 3 tests: covering F1") {
		t.Fatalf("line = %q, want it to say what was selected", line)
	}
	var recorded bool
	for _, e := range ReadEvents(root) {
		if e.Detail["selected"] == "1" && e.Detail["total"] == "3" {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("no event records selected 1 of total 3: %+v", ReadEvents(root))
	}
}

func TestPostEdit_TestSelectRunsTheWholePackageWhenTheEditCannotBeMapped(t *testing.T) {
	cases := []struct {
		name   string
		edited string
		query  CoverQuery
		reason string
		asked  bool
	}{
		{"a var edit", strings.Replace(selectOld, "limit = 3", "limit = 4", 1), CoverQuery{Fresh: true, Tests: []string{"TestOne"}, Total: 3}, "non-function declaration", false},
		{"a store that is not fresh", strings.Replace(selectOld, "return 1", "return 11", 1), CoverQuery{Total: 3, Reason: "no coverage store for internal/p yet"}, "no coverage store for internal/p yet", true},
		{"every test covers the edit", strings.Replace(selectOld, "return 1", "return 11", 1), CoverQuery{Fresh: true, Tests: []string{"TestA", "TestB", "TestC"}, Total: 3}, "whole package", true},
	}
	for _, c := range cases {
		_, target := selectProject(t, "edit", "p.go", c.edited)
		asked := selectSeams(t, selectOld, c.query)
		seen, line := editAndSee(t, target)
		if want := []string{"test", "./internal/p"}; !slices.Equal(seen.Args, want) {
			t.Errorf("%s: args = %q, want the argv unchanged", c.name, seen.Args)
		}
		if !strings.Contains(line, "full suite: ") || !strings.Contains(line, c.reason) || strings.Contains(line, "selected") {
			t.Errorf("%s: line = %q, want \"full suite: ...%s\"", c.name, line, c.reason)
		}
		if (len(*asked) > 0) != c.asked {
			t.Errorf("%s: queried = %v, want %v", c.name, *asked, c.asked)
		}
	}
}

func TestPostEdit_TestSelectRunsEveryTestOfAnEditedTestFile(t *testing.T) {
	_, target := selectProject(t, "edit", "p_test.go", strings.Replace(selectTestsOld, "_ = F2()", "_ = F2() + 1", 1))
	asked := selectSeams(t, selectTestsOld, CoverQuery{Total: 3, Reason: "no function was named"})
	seen, line := editAndSee(t, target)
	if want := []string{"test", "./internal/p", "-run=^(TestOne|TestTwo)$"}; !slices.Equal(seen.Args, want) {
		t.Fatalf("args = %q, want %q", seen.Args, want)
	}
	if !strings.Contains(line, "selected 2 of 3 tests: covering p_test.go") {
		t.Fatalf("line = %q", line)
	}
	if len(*asked) != 1 || len((*asked)[0]) != 1 {
		t.Fatalf("queries = %v, want one for the package total and no function", *asked)
	}
}

func TestPostEdit_TestSelectRunsTheWholePackageForATestHelperEdit(t *testing.T) {
	_, target := selectProject(t, "edit", "p_test.go", strings.Replace(selectTestsOld, "return 1 }", "return 2 }", 1))
	selectSeams(t, selectTestsOld, CoverQuery{Total: 3, Reason: "no function was named"})
	seen, line := editAndSee(t, target)
	if want := []string{"test", "./internal/p"}; !slices.Equal(seen.Args, want) || !strings.Contains(line, "full suite: the test helper helper changed") {
		t.Fatalf("args = %q, line = %q", seen.Args, line)
	}
}

func TestPostEdit_TestSelectLeavesAWriteOfSeveralFilesWhole(t *testing.T) {
	root, target := selectProject(t, "edit", "p.go", strings.Replace(selectOld, "return 1", "return 11", 1))
	asked := selectSeams(t, selectOld, CoverQuery{Fresh: true, Tests: []string{"TestOne"}, Total: 3})
	base := Runner{Cmd: "go", Args: []string{"test", "./internal/p"}}
	got := withTestSelect(base, root, target, []string{filepath.Join(filepath.Dir(target), "q.go")})
	if !slices.Equal(got.Args, base.Args) || got.Select == nil || !strings.Contains(got.Select.Reason, "several files") {
		t.Fatalf("args = %q, select = %+v", got.Args, got.Select)
	}
	if len(*asked) != 0 {
		t.Fatalf("queried %v", *asked)
	}
}

// Every line a selected green is shown in names the selection: the plain
// advisory, the unconstrained one, and never a bare green.
func TestSelectionNote_ReachesBothGreenLines(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./p", "-run=^(TestOne)$"}, Select: &Selection{Run: 1, Total: 3, Funcs: []string{"F1"}}}
	out := "--- PASS: TestOne (0.00s)\nPASS\n"
	if got := passAdvisory(r, "/r", Green, out, 1200*time.Millisecond, nil); !strings.Contains(got, "green (1 passed, selected 1 of 3 tests: covering F1, 1.2s)") {
		t.Fatalf("advisory = %q", got)
	}
	if got := unconstrainedLine(r, "/r", 1, 0, time.Second); !strings.Contains(got, "(1 passed, selected 1 of 3 tests: covering F1; no test changed") {
		t.Fatalf("unconstrained = %q", got)
	}
	whole := Runner{Cmd: "go", Args: []string{"test", "./p"}, Select: &Selection{Total: 3, Reason: "no coverage store for p yet"}}
	if got := passAdvisory(whole, "/r", Green, out, 1200*time.Millisecond, nil); !strings.Contains(got, "green (1 passed, full suite: no coverage store for p yet, 1.2s)") {
		t.Fatalf("advisory = %q", got)
	}
	plain := Runner{Cmd: "go", Args: []string{"test", "./p"}}
	if got := passAdvisory(plain, "/r", Green, out, 1200*time.Millisecond, nil); !strings.Contains(got, "green (1 passed, 1.2s)") {
		t.Fatalf("a run with no selection says %q", got)
	}
}

// A run that names several packages, or a package the edited file is not in,
// is not the edit's own package: nothing is selected for it.
func TestWithTestSelect_LeavesARunOfAnotherShapeWhole(t *testing.T) {
	root, target := selectProject(t, "edit", "p.go", strings.Replace(selectOld, "return 1", "return 11", 1))
	asked := selectSeams(t, selectOld, CoverQuery{Fresh: true, Tests: []string{"TestOne"}, Total: 3})
	for _, c := range []struct {
		args   []string
		reason string
	}{
		{[]string{"test", "./internal/p", "./internal/q"}, "not one go package"},
		{[]string{"test", "./internal/q"}, "not a go file of the package"},
		{[]string{"test", "./..."}, "not a go file of the package"},
	} {
		base := Runner{Cmd: "go", Args: c.args}
		got := withTestSelect(base, root, target, nil)
		if !slices.Equal(got.Args, c.args) || got.Select == nil || !strings.Contains(got.Select.Reason, c.reason) {
			t.Errorf("%v: args = %q, select = %+v, want the argv kept and %q", c.args, got.Args, got.Select, c.reason)
		}
	}
	if len(*asked) != 0 {
		t.Fatalf("queried %v", *asked)
	}
}

// A selected run that go also served from its cache says both.
func TestSelectionNote_SitsBesideTheCachedPackages(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./p", "-run=^(TestOne)$"}, Select: &Selection{Run: 1, Total: 3, Funcs: []string{"F1"}}}
	out := "--- PASS: TestOne (0.00s)\nok  \texample.com/p\t(cached)\n"
	if got := greenLabelFor(r, Green, out, time.Second); got != "green (1 passed (1 package cached), selected 1 of 3 tests: covering F1, 1.0s)" {
		t.Fatalf("label = %q", got)
	}
	if got := selectionDetail(r); got["selected"] != "1" || got["total"] != "3" {
		t.Fatalf("detail = %v", got)
	}
	if got := selectionDetail(Runner{}); got != nil {
		t.Fatalf("a run test-select did not look at records %v", got)
	}
}

// Names the store gave that the build does not hold (a test file a tag leaves
// out) select nothing: that is not a green. The package runs whole once, and
// the line says the selection ran none (the ladder's first rung).
func TestPostEdit_ASelectionThatRanNoTestRunsThePackageWhole(t *testing.T) {
	_, target := selectProject(t, "edit", "p.go", strings.Replace(selectOld, "return 1", "return 11", 1))
	selectSeams(t, selectOld, CoverQuery{Fresh: true, Tests: []string{"TestGone"}, Total: 3})
	var runs []Runner
	line := PostEdit(postPayload("Edit", target), func(r Runner, _ string) SuiteResult {
		runs = append(runs, r)
		if len(runs) == 1 {
			return SuiteResult{Passed: true, Output: "testing: warning: no tests to run\nPASS\nok  \texample.com/p\t0.002s [no tests to run]\n"}
		}
		return SuiteResult{Passed: true, Output: "=== RUN   TestOne\n--- PASS: TestOne (0.00s)\nPASS\nok  \texample.com/p\t0.01s\n"}
	})
	if len(runs) != 2 || !slices.Equal(runs[1].Args, []string{"test", "./internal/p"}) {
		t.Fatalf("runs = %+v, want the selection and then the whole package", runs)
	}
	if !strings.Contains(line, "full suite: the selected tests ran none") || strings.Contains(line, "selected 1 of 3") {
		t.Fatalf("line = %q", line)
	}
}

// The deferred path persists the selection with the job: a harvested green
// still says which tests it ran, the event records selected and total, and a
// run that fell back to the whole package records why.
func TestPostEdit_ADeferredSelectedRunKeepsItsSelection(t *testing.T) {
	root, target := selectProject(t, "edit", "p.go", strings.Replace(selectOld, "return 1", "return 11", 1))
	selectSeams(t, selectOld, CoverQuery{Fresh: true, Tests: []string{"TestOne"}, Total: 3})
	selected := Runner{Cmd: "go", Args: []string{"test", "./internal/p", "-run=^(TestOne)$"}}
	key := phaseKey(phaseArgv(selected, "run"))
	scriptedPhases(t, map[string]scriptedPhase{
		key: {out: &PhaseOutcome{ExitCode: 0}, log: "=== RUN   TestOne\n--- PASS: TestOne (0.00s)\nPASS\nok  \texample.com/p\t0.01s\n"},
	})
	line := PostEdit(postPayload("Edit", target), fakeRun(true, "the foreground runner must not be used"))
	if !strings.Contains(line, "selected 1 of 3 tests: covering F1") {
		t.Fatalf("deferred line = %q, want the selection named", line)
	}
	var recorded bool
	for _, e := range ReadEvents(root) {
		if e.Detail["selected"] == "1" && e.Detail["total"] == "3" {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("no event records the deferred selection: %+v", ReadEvents(root))
	}
}

func TestSelectionDetail_ARefusedSelectionRecordsWhyItRanWhole(t *testing.T) {
	got := selectionDetail(Runner{Select: &Selection{Total: 3, Reason: "no coverage store for p yet"}})
	if got["full_reason"] != "no coverage store for p yet" || got["selected"] != "" {
		t.Fatalf("detail = %v", got)
	}
}

// The stale-result label of a selected run names the selection as well.
func TestStaleVerdictLabel_NamesASelection(t *testing.T) {
	log := filepath.Join(t.TempDir(), "run.log")
	mustWrite(t, log, "=== RUN   TestOne\n--- PASS: TestOne (0.00s)\nPASS\nok  \texample.com/p\t0.01s\n")
	j := DeferredJob{Phase: "run", Log: log, Runner: []string{"go", "test", "./p", "-run=^(TestOne)$"}, Select: &Selection{Run: 1, Total: 3, Funcs: []string{"F1"}}}
	if got := staleVerdictLabel(j, PhaseOutcome{ExitCode: 0}); !strings.Contains(got, "selected 1 of 3") {
		t.Fatalf("label = %q", got)
	}
}

// A selected run harvested at a LATER hook is judged from the stored job alone:
// the job must carry the selection, or the green reads as a full one.
func TestEditResultAdvisory_AHarvestedJobKeepsItsSelection(t *testing.T) {
	root, target := selectProject(t, "edit", "p.go", selectOld)
	selected := Runner{Cmd: "go", Args: []string{"test", "./internal/p", "-run=^(TestOne)$"}, Select: &Selection{Run: 1, Total: 3, Funcs: []string{"F1"}}}
	j := firstEditPhase(selected, root, target, "head", "hash", "sess-harvest", "")
	saveDeferredJob(j)
	j, ok := loadDeferredJob("sess-harvest", root)
	if !ok || j.Select == nil || j.Select.Run != 1 {
		t.Fatalf("the stored job lost its selection: %+v", j.Select)
	}
	mustWrite(t, j.Log, "=== RUN   TestOne\n--- PASS: TestOne (0.00s)\nPASS\nok  \texample.com/p\t0.01s\n")
	line := editResultAdvisory(j, PhaseOutcome{ExitCode: 0}, root, nil, "", "head")
	if !strings.Contains(line, "selected 1 of 3 tests: covering F1") {
		t.Fatalf("harvested line = %q", line)
	}
	var recorded bool
	for _, e := range ReadEvents(root) {
		if e.Detail["selected"] == "1" && e.Detail["total"] == "3" {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("no event records the harvested selection: %+v", ReadEvents(root))
	}
}

// A queued run carries its selection to the job it becomes.
func TestEnqueueRun_ASelectionSurvivesTheQueue(t *testing.T) {
	root, _ := selectProject(t, "edit", "p.go", selectOld)
	sel := &Selection{Run: 1, Total: 3, Funcs: []string{"F1"}}
	enqueueRun("sess-queue", root, queuedRun{Runner: []string{"go", "test", "./internal/p", "-run=^(TestOne)$"}, Dir: root, File: "p.go", Select: sel, At: time.Now()})
	q := readQueue(queuePath("sess-queue", root))
	if len(q.Runs) != 1 || q.Runs[0].Select == nil || q.Runs[0].Select.Run != 1 {
		t.Fatalf("queue = %+v", q)
	}
}
