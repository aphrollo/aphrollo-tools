package tdd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// writeOutcomes puts a cargo-mutants outcomes file where a run in root would
// have left one, from the mutants it names.
func writeOutcomes(t *testing.T, root string, mutants ...MutantOutcome) {
	t.Helper()
	type span struct {
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
	}
	type mutant struct {
		Name    string `json:"name"`
		Package string `json:"package"`
		File    string `json:"file"`
		Span    span   `json:"span"`
	}
	type scenario struct {
		Mutant mutant `json:"Mutant"`
	}
	type outcome struct {
		Scenario scenario `json:"scenario"`
		Summary  string   `json:"summary"`
	}
	summary := map[string]string{
		"caught": "CaughtMutant", "missed": "MissedMutant",
		"unviable": "Unviable", "timeout": "Timeout",
	}
	doc := struct {
		Outcomes []outcome `json:"outcomes"`
	}{}
	for _, m := range mutants {
		var o outcome
		o.Summary = summary[m.Status]
		o.Scenario.Mutant = mutant{Name: mutantLineOf(m.File, m.Line, m.Col, m.Mutation), Package: m.Package, File: m.File}
		o.Scenario.Mutant.Span.Start.Line = m.Line
		o.Scenario.Mutant.Span.Start.Column = m.Col
		doc.Outcomes = append(doc.Outcomes, o)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "mutants.out", "outcomes.json"), string(data))
}

// makeStagedMergeRepo builds the state the pre-merge-commit hook actually
// sees: a base commit on trunk, a lane commit on a branch, and that lane
// merged with --no-commit, so HEAD is still the base commit and the lane's
// change exists only in the index and the worktree. It answers the root and
// the merge base, which is what the stage measures against.
func makeStagedMergeRepo(t *testing.T, lane map[string]string) (root, base string) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	writeMeasureBase(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	trunk := strings.TrimSpace(gitOutT(t, root, "rev-parse", "--abbrev-ref", "HEAD"))
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	for rel, content := range lane {
		write(t, root, rel, content)
	}
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	gitDo(t, root, "checkout", "-q", trunk)
	base = strings.TrimSpace(gitOutT(t, root, "merge-base", "HEAD", "lane"))
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "-q", "lane")
	return root, base
}

// A source file edited but not committed is part of what the merge lands, and
// a selection that only reads commits cannot see it. The measurement is of
// the tree, not of the history that led to it.
func TestMeasureDiff_UncommittedSourceChangeIsMeasured(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := makeMeasureRepo(t, map[string]string{"README.md": "lane\n"})
	write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a * b }\n")

	files, crates, err := measureDiff(root, base)

	if err != nil {
		t.Fatalf("measureDiff: %v", err)
	}
	if len(files) != 1 || files[0] != "crates/a/src/lib.rs" {
		t.Fatalf("files = %v, want the uncommitted source change", files)
	}
	if len(crates) != 1 || crates[0] != "a" {
		t.Errorf("crates = %v, want the crate owning it", crates)
	}
}

// cargo-mutants mutates the tree IN PLACE and restores it as it goes; a run
// that is killed leaves a mutation behind. Merging that is merging a mutant.
// The tree is compared with what it looked like before the run — never
// against a clean tree, since a staged merge is legitimately dirty — and a
// difference refuses, naming the files and how to put them back. It is never
// restored automatically: a tool that rewrites source on its way out is not
// one anybody can reason about mid-incident.
func TestMeasure_RunThatLeavesTheTreeChangedIsRefused(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeMeasureRepo(t, laneSource)
	stubMutantsExec(t, func(int, measuredCall) (int, error) {
		// Every mutant caught, and the mutation for the last one still in
		// the file: the outcomes say the lane is fine and the tree says it
		// is not this lane's tree any more.
		writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace - with +", Package: "a", Status: "caught"})
		write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { 0 }\n")
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Jobs: 1})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("a run that left a mutation in the tree must refuse, got %+v", v)
	}
	first := strings.SplitN(v.Message, "\n", 2)[0]
	if first != "mutants: the run left the working tree changed:" {
		t.Errorf("first line = %q, want the finding", first)
	}
	if !strings.Contains(v.Message, "crates/a/src/lib.rs") {
		t.Errorf("message = %q, want the changed file named", v.Message)
	}
	if !strings.Contains(v.Message, "git checkout -- crates/a/src/lib.rs") {
		t.Errorf("message = %q, want the exact restore command", v.Message)
	}
	if got := readFileString(t, filepath.Join(root, "crates/a/src/lib.rs")); !strings.Contains(got, "0 }") {
		t.Errorf("source = %q, want it left exactly as the run left it — never restored behind the operator's back", got)
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-refused:tree-changed") {
		t.Errorf("gate.log has no tree-changed line:\n%s", gateLogText(t, cfgDir))
	}
}

// gremlins' NOT COVERED is not "a test ran and did not notice" — it is "no
// coverage block maps here", which on Windows it reports for every mutant in
// the module. Folded into unviable it disappears; counted apart, a report
// that is mostly uncovered says so.
func TestJudge_NotCoveredIsCountedApartFromUnviable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(setMutantsGOOSForTest("linux"))
	stubMutantsExec(t, func(int, measuredCall) (int, error) {
		mustWrite(t, gremlinsReportPath(root), `{"files":[{"file_name":"calc.go","mutations":[
			{"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":20},
			{"type":"CONDITIONALS_BOUNDARY","status":"NOT COVERED","line":3,"column":26}]}]}`)
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Jobs: 2})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.NotCovered != 1 || v.Unviable != 0 || v.Caught != 1 {
		t.Fatalf("verdict = %+v, want 1 caught, 1 not covered and nothing called unviable", v)
	}
	want := "mutants: 2 tested, 1 caught, 0 unviable, 0 missed (0 accepted), 0 unmeasured, 1 not covered"
	if !strings.Contains(v.Message, want) {
		t.Errorf("message = %q, want the summary %q", v.Message, want)
	}
}

// `gate stats` answers "how many merges did this stage refuse, and for what"
// off the gate log alone. That only works if the line carries the verdict and
// the counts, so both are asserted literally: swap passed for refused, or
// drop a count, and this fails.
func TestMeasure_GateLogCarriesTheVerdictAndItsCounts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		want   string
	}{
		{"passed", "caught", "mutants-passed:tested=1,caught=1,unviable=0,missed=0,accepted=0,unmeasured=0,notcovered=0"},
		{"refused", "missed", "mutants-refused:tested=1,caught=0,unviable=0,missed=1,accepted=0,unmeasured=0,notcovered=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgDir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
			t.Cleanup(SetFreeSpaceForTest(999, true))
			root, base := makeMeasureRepo(t, laneSource)
			stubMutantsExec(t, func(int, measuredCall) (int, error) {
				writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
					Mutation: "replace + with -", Package: "a", Status: tc.status})
				return 0, nil
			})

			if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Jobs: 1}); err != nil {
				t.Fatalf("MeasureLane: %v", err)
			}

			if !strings.Contains(gateLogText(t, cfgDir), tc.want) {
				t.Errorf("gate.log has no %q:\n%s", tc.want, gateLogText(t, cfgDir))
			}
		})
	}
}
