package mutation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Issue #910. A not-covered or inconclusive mutant used to pass whatever line
// it sat on, so a gap in code the lane itself wrote merged untested and
// became a follow-up nobody picked up. With mutants-at-merge on, such a
// mutant on a line the diff adds or changes is refused by name, in the
// accept-list's own form; on a line the diff did not touch it is reported
// exactly as before.

// uncoveredOnAnAddedLine is one gremlins NOT COVERED mutant the measured diff
// added the line of.
func uncoveredOnAnAddedLine() MutantOutcome {
	return MutantOutcome{File: "pkg/a.go", Line: 12, Col: 7, Mutation: "CONDITIONALS_NEGATION",
		Status: gremlinsNotCovered, NewLine: true}
}

func TestJudgeMutants_RefusesANotCoveredMutantOnALineTheDiffAdds(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{AtMerge: true}, []MutantOutcome{uncoveredOnAnAddedLine()})

	if !v.Refused {
		t.Fatalf("a mutant no test runs, on a line this lane wrote, was let through:\n%s", v.Message)
	}
	if !strings.HasPrefix(v.Message, "pkg/a.go:12:7 CONDITIONALS_NEGATION (not covered)\n") {
		t.Errorf("message = %q, want it to lead with the mutant in the accept-list form", v.Message)
	}
	if !strings.Contains(v.Message, gapRemedy) {
		t.Errorf("message = %q, want the remedy for a gap", v.Message)
	}
	if len(v.Gaps) != 1 {
		t.Errorf("Gaps = %+v, want the one refused mutant", v.Gaps)
	}
}

func TestJudgeMutants_ReportsTheSameMutantOnAnUntouchedLineWithoutRefusing(t *testing.T) {
	t.Parallel()
	m := uncoveredOnAnAddedLine()
	m.NewLine = false
	v := judgeMutants(MutantsConfig{AtMerge: true}, []MutantOutcome{m})

	if v.Refused {
		t.Fatalf("a not-covered mutant on a line the diff did not touch was refused:\n%s", v.Message)
	}
	if v.NotCovered != 1 || !strings.Contains(v.Message, "1 not covered") {
		t.Errorf("verdict = %+v, want it counted and reported as today", v)
	}
	if strings.Contains(v.Message, gapRemedy) {
		t.Errorf("message = %q, want no gap remedy where nothing was refused", v.Message)
	}
}

func TestJudgeMutants_AnAcceptedNotCoveredMutantOnAnAddedLinePasses(t *testing.T) {
	t.Parallel()
	cfg := MutantsConfig{AtMerge: true, Accept: []string{
		"pkg/a.go:12:7 CONDITIONALS_NEGATION # kind=unobservable-capability issue=#1: no test can reach the probe yet",
	}}
	v := judgeMutants(cfg, []MutantOutcome{uncoveredOnAnAddedLine()})

	if v.Refused {
		t.Fatalf("an accepted entry did not admit the mutant it names:\n%s", v.Message)
	}
	if !strings.Contains(v.Message, "pkg/a.go:12:7 CONDITIONALS_NEGATION (not covered)") {
		t.Errorf("message = %q, want the admitted mutant still named", v.Message)
	}
}

func TestJudgeMutants_AnExemptNotCoveredMutantOnAnAddedLinePasses(t *testing.T) {
	t.Parallel()
	m := uncoveredOnAnAddedLine()
	m.Exempt = "a package-level declaration"
	v := judgeMutants(MutantsConfig{AtMerge: true}, []MutantOutcome{m})

	if v.Refused {
		t.Fatalf("a mutant no coverage tool could ever report was refused:\n%s", v.Message)
	}
}

func TestJudgeMutants_WithoutMutantsAtMergeAnAddedLineGapIsOnlyReported(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{}, []MutantOutcome{uncoveredOnAnAddedLine()})

	if v.Refused {
		t.Fatalf("a repo that did not declare mutants-at-merge was refused over a gap:\n%s", v.Message)
	}
}

// An inconclusive mutant reaching the judge on an added line is one the
// resolution could not settle; it is refused as unresolved, carrying why,
// and never called a survivor.
func TestJudgeMutants_RefusesAnUnresolvedInconclusiveMutantOnAnAddedLine(t *testing.T) {
	t.Parallel()
	m := MutantOutcome{File: "pkg/b.go", Line: 4, Col: 9, Mutation: "ARITHMETIC_BASE", Status: gremlinsScopeUnknown,
		NewLine: true, Note: "UNRESOLVED: the tests of pkg/c did not finish within 2m0s"}
	v := judgeMutants(MutantsConfig{AtMerge: true}, []MutantOutcome{m})

	if !v.Refused {
		t.Fatalf("an unresolved inconclusive mutant on an added line was let through:\n%s", v.Message)
	}
	if !strings.Contains(v.Message, "pkg/b.go:4:9 ARITHMETIC_BASE (inconclusive) — UNRESOLVED: the tests of pkg/c") {
		t.Errorf("message = %q, want it named as inconclusive with its unresolved reason", v.Message)
	}
	if len(v.Unaccepted) != 0 || v.Missed != 0 {
		t.Errorf("verdict = %+v, want no survivor claimed for a mutant nobody settled", v)
	}
	if strings.Count(v.Message, "pkg/b.go:4:9") != 1 {
		t.Errorf("message = %q, want the mutant named once", v.Message)
	}
}

// The measurement end to end: the lane's diff, gremlins' report (a fixture —
// gremlins itself is not run here) and the judge, over a real module.

// laneOverTorque is the torque module committed as the base, then one more
// commit adding path with content, measured against the base.
func laneOverTorque(t *testing.T, path, content string) (root, base string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	t.Cleanup(SetMutantsGOOSForTest("linux"))
	t.Cleanup(setMutantsJobsForTest(1, "pinned"))
	root, _ = torqueAndItsImporter(t)
	base = strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD"))
	write(t, root, filepath.FromSlash(path), content)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	return root, base
}

// gauge.go is the lane's own new file: line 5 is a package-level var
// initializer, line 9 a comparison in a function no test calls.
const gaugeSource = "package driveline\n\nimport \"time\"\n\n" +
	"var probeTimeout = 10 * time.Second\n\n" +
	"// Overloaded says whether a reading exceeds the rated torque.\n" +
	"func Overloaded(reading float64) bool {\n\treturn reading > 400\n}\n"

func notCoveredReport(file string, line, col int, mutator string) string {
	return fmt.Sprintf(`{"files":[{"file_name":%q,"mutations":[{"type":%q,"status":"NOT COVERED","line":%d,"column":%d}]}]}`,
		file, mutator, line, col)
}

func TestMeasure_RefusesANotCoveredMutantOnALineTheLaneAdds(t *testing.T) {
	root, base := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	stubGremlinsReport(t, root, notCoveredReport("driveline/gauge.go", 9, 17, "CONDITIONALS_BOUNDARY"))

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused || !strings.Contains(v.Message, "driveline/gauge.go:9:17 CONDITIONALS_BOUNDARY (not covered)") {
		t.Fatalf("a comparison the lane added and no test runs was not refused by name:\n%s", v.Message)
	}
}

func TestMeasure_ReportsTheSameGapOnALineTheLaneDidNotTouch(t *testing.T) {
	root, base := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	// driveline.go:8 is `return left + right`, committed before the base.
	stubGremlinsReport(t, root, notCoveredReport("driveline/driveline.go", 8, 14, "ARITHMETIC_BASE"))

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("a not-covered mutant on a line the lane did not touch was refused:\n%s", v.Message)
	}
	if v.NotCovered != 1 {
		t.Errorf("NotCovered = %d, want the mutant still counted", v.NotCovered)
	}
}

// Go coverage never attributes a package-level initializer to a test, so a
// NOT COVERED there says nothing a test could change.
func TestMeasure_AVarInitializerTheLaneAddsPasses(t *testing.T) {
	root, base := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	stubGremlinsReport(t, root, notCoveredReport("driveline/gauge.go", 5, 23, "ARITHMETIC_BASE"))

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("a var initializer was refused as a gap no test could close:\n%s", v.Message)
	}
}

// A file this platform's build leaves out is NOT COVERED here whatever its
// tests do: no run on this box compiles it.
func TestMeasure_AGapInAFileOutsideThisPlatformsBuildPasses(t *testing.T) {
	other := "gauge_windows.go"
	if runtime.GOOS == "windows" {
		other = "gauge_linux.go"
	}
	root, base := laneOverTorque(t, "driveline/"+other, gaugeSource)
	stubGremlinsReport(t, root, notCoveredReport("driveline/"+other, 9, 17, "CONDITIONALS_BOUNDARY"))

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("a mutant in a file this platform never builds was refused:\n%s", v.Message)
	}
}

// Trap (b) on a line the lane adds: the survivor gremlins judged with
// torque's own tests is run against driveline's, which kill it.
func TestMeasure_AnInconclusiveMutantOnAnAddedLineIsSettledByTheTestsThatReachIt(t *testing.T) {
	root, base := measurableTorqueLane(t)
	stubGremlinsReport(t, root, livedInTorque)

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("a mutant driveline's test kills was refused:\n%s", v.Message)
	}
	if v.Caught != 1 || !strings.Contains(v.Message, "killed by the tests of example.com/m/driveline (") {
		t.Errorf("verdict = %+v, want it caught and the killing package named\n%s", v, v.Message)
	}
}

// The same survivor, when the tests that reach it cannot finish inside the
// budget: refused as unresolved, never as a survivor.
func TestMeasure_AnInconclusiveMutantTheBudgetCutsOffIsRefusedAsUnresolved(t *testing.T) {
	root, base := measurableTorqueLane(t)
	stubGremlinsReport(t, root, livedInTorque)
	t.Cleanup(setResolveBudgetForTest(time.Nanosecond))

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused || !strings.Contains(v.Message, "torque/torque.go:5:16 ARITHMETIC_BASE (inconclusive) — UNRESOLVED:") {
		t.Fatalf("an unresolved mutant on an added line was not refused as unresolved:\n%s", v.Message)
	}
	if v.Missed != 0 {
		t.Errorf("Missed = %d, want no survivor claimed", v.Missed)
	}
}

// A reach nobody could read settles nothing: on a line the lane adds, the
// mutant is refused as UNRESOLVED with the reader's own words, and no
// survivor is claimed.
func TestMeasure_AnInconclusiveMutantWhoseReachCannotBeReadIsRefusedAsUnresolved(t *testing.T) {
	root, base := measurableTorqueLane(t)
	stubGremlinsReport(t, root, livedInTorque)
	setGoReachGraphForTest(t, func(string) (goReachGraph, error) {
		return goReachGraph{}, errors.New("go list in /repo: exit status 1: go.mod names no module")
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused || !strings.Contains(v.Message, "(inconclusive) — UNRESOLVED: the module's own package graph could not be read") {
		t.Fatalf("an unsettled mutant on an added line was not refused as unresolved:\n%s", v.Message)
	}
	if v.Missed != 0 {
		t.Errorf("Missed = %d, want no survivor claimed", v.Missed)
	}
}

// A mutant settled by running it is reported with how it was settled, so a
// reader sees the verdict was earned rather than assumed.
func TestJudgeMutants_ReportsEachSettledMutantWithItsReason(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{AtMerge: true}, []MutantOutcome{
		{File: "a.go", Line: 1, Col: 2, Mutation: "ARITHMETIC_BASE", Name: "a.go:1:2: ARITHMETIC_BASE",
			Status: "unviable", NewLine: true, Note: "does not compile, so no test can run it"},
		{File: "a.go", Line: 3, Col: 4, Mutation: "CONDITIONALS_BOUNDARY", Name: "a.go:3:4: CONDITIONALS_BOUNDARY",
			Status: "caught", NewLine: true, Note: "killed by the tests of p"},
		{File: "a.go", Line: 5, Col: 6, Mutation: "CONDITIONALS_BOUNDARY", Name: "a.go:5:6: CONDITIONALS_BOUNDARY",
			Status: "caught"},
	})

	for _, want := range []string{
		"a.go:1:2: ARITHMETIC_BASE — does not compile, so no test can run it\n",
		"a.go:3:4: CONDITIONALS_BOUNDARY — killed by the tests of p\n",
	} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message = %q, want %q", v.Message, want)
		}
	}
	if strings.Contains(v.Message, "a.go:5:6") {
		t.Errorf("message = %q, want a mutant gremlins itself caught left out", v.Message)
	}
}

// An inconclusive mutant on an untouched line is still printed with its
// reason beside a refused one on an added line.
func TestJudgeMutants_PrintsAnUntouchedInconclusiveMutantBesideARefusedOne(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{AtMerge: true}, []MutantOutcome{
		{File: "a.go", Line: 1, Col: 2, Mutation: "ARITHMETIC_BASE", Status: gremlinsScopeUnknown, Note: "old reach"},
		{File: "a.go", Line: 3, Col: 4, Mutation: "ARITHMETIC_BASE", Status: gremlinsScopeUnknown, NewLine: true,
			Note: "UNRESOLVED: new"},
	})

	if !strings.Contains(v.Message, " — old reach\n") {
		t.Errorf("message = %q, want the untouched inconclusive mutant printed", v.Message)
	}
}

func TestParseAddedLines_ReadsEachHunksNewSide(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/p/a.go b/p/a.go\n--- a/p/a.go\n+++ b/p/a.go\n" +
		"@@ -2 +2 @@ func f() {\n-\told\n+\tnew\n" +
		"@@ -9,0 +10,3 @@ func g() {\n+\ta\n+\tb\n+\tc\n" +
		"@@ -20,2 +22,0 @@\n-\tgone\n-\tgone\n" +
		"diff --git a/p/gone.go b/p/gone.go\n--- a/p/gone.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-x\n-y\n" +
		"diff --git a/p/b.go b/p/b.go\n--- /dev/null\n+++ b/p/b.go\n@@ -0,0 +1,2 @@\n+package p\n+\n"

	got := parseAddedLines(diff)

	want := map[string]map[int]bool{
		"p/a.go": {2: true, 10: true, 11: true, 12: true},
		"p/b.go": {1: true, 2: true},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("parseAddedLines = %v, want %v", got, want)
	}
}

// Which lines are new is only known from a diff git could produce; without
// one nothing is refused, and nothing is settled.
func TestMeasure_AnUnreadableDiffRefusesNoGap(t *testing.T) {
	root, base := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	stubGremlinsReport(t, root, notCoveredReport("driveline/gauge.go", 9, 17, "CONDITIONALS_BOUNDARY"))
	prev := diffAddedLinesFn
	diffAddedLinesFn = func(string, string) (map[string]map[int]bool, error) {
		return nil, errors.New("git diff -U0: fatal: bad revision")
	}
	t.Cleanup(func() { diffAddedLinesFn = prev })
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("a gap was refused although which lines are new could not be read:\n%s", v.Message)
	}
	if !strings.Contains(log.String(), "fatal: bad revision") {
		t.Errorf("log = %q, want git's own words for why the diff could not be read", log.String())
	}
}

// Without mutants-at-merge nothing is marked and nothing is run: the
// inconclusive survivor stays exactly as the run reported it.
func TestMeasure_WithoutMutantsAtMergeNothingIsSettled(t *testing.T) {
	root, base := measurableTorqueLane(t)
	stubGremlinsReport(t, root, livedInTorque)

	v, err := MeasureLane(root, MutantsConfig{}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if len(v.Inconclusive) != 1 || v.Caught != 0 || v.Inconclusive[0].NewLine {
		t.Fatalf("verdict = %+v, want the survivor left inconclusive and unmarked", v)
	}
}

func TestClassifyNotCovered_ExemptsOnlyWhatNoTestCouldCover(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := "package p\n\nvar limit = 2 * 3\n\n" +
		"func f(s string, n int) (string, int) {\n" +
		"\tswitch {\n\tcase n > limit:\n\t\treturn \"#\" + s, n + 1\n\t}\n\treturn s, n\n}\n"
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		line, col int
		mutator   string
		exempt    bool
		gap       bool
	}{
		{3, 15, "ARITHMETIC_BASE", true, false},      // var initializer
		{7, 9, "CONDITIONALS_BOUNDARY", false, true}, // case condition
		{8, 14, "ARITHMETIC_BASE", true, false},      // "#" + s
		{8, 21, "ARITHMETIC_BASE", false, false},     // n + 1
	} {
		m := MutantOutcome{File: "p.go", Line: c.line, Col: c.col, Mutation: c.mutator, Status: gremlinsNotCovered}
		exempt, gap := classifyNotCovered(root, m)
		if (exempt != "") != c.exempt || gap != c.gap {
			t.Errorf("%d:%d %s: exempt %q, gap %v; want exempt %v, gap %v",
				c.line, c.col, c.mutator, exempt, gap, c.exempt, c.gap)
		}
	}
}
