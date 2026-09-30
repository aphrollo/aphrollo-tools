package mutation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shardReport(tree string, shard, shards int, base string, mutants ...MutantOutcome) RunnerReport {
	return RunnerReport{Tree: tree, Shard: shard, Shards: shards, Base: base, Mutants: mutants}
}

func TestShardReportsProblem_AcceptsExactlyTheWholeMeasurement(t *testing.T) {
	t.Parallel()
	const tree = "abc"
	cases := []struct {
		name    string
		reports []RunnerReport
		shards  int
		want    string // "" for none, else a substring of the problem
	}{
		{"one shard of one", []RunnerReport{shardReport(tree, 0, 1, "b")}, 1, ""},
		{"two shards of two", []RunnerReport{shardReport(tree, 1, 2, "b"), shardReport(tree, 0, 2, "b")}, 2, ""},
		{"no reports", nil, 1, "0 shard report(s) arrived and 1 were expected"},
		{"one report short", []RunnerReport{shardReport(tree, 0, 2, "b")}, 2, "1 shard report(s) arrived and 2 were expected"},
		{"one report over", []RunnerReport{shardReport(tree, 0, 1, "b"), shardReport(tree, 0, 1, "b")}, 1,
			"2 shard report(s) arrived and 1 were expected"},
		{"a count of zero expects nothing", nil, 0, "at least 1"},
		{"a report that names another shard count", []RunnerReport{shardReport(tree, 0, 3, "b"), shardReport(tree, 1, 2, "b")}, 2,
			"one of 3 shards and 2 were expected"},
		{"an index at the count", []RunnerReport{shardReport(tree, 0, 2, "b"), shardReport(tree, 2, 2, "b")}, 2, "shard 2 of 2"},
		{"a negative index", []RunnerReport{shardReport(tree, -1, 2, "b"), shardReport(tree, 0, 2, "b")}, 2, "shard -1 of 2"},
		{"the same shard twice", []RunnerReport{shardReport(tree, 1, 2, "b"), shardReport(tree, 1, 2, "b")}, 2, "two reports are shard 1"},
		{"a report of another tree", []RunnerReport{shardReport(tree, 0, 2, "b"), shardReport("other", 1, 2, "b")}, 2,
			"shard 1: it measured tree other"},
		{"a report naming no tree", []RunnerReport{shardReport(tree, 0, 2, "b"), shardReport("", 1, 2, "b")}, 2, "shard 1: the report names no tree"},
		{"a report measured against another base", []RunnerReport{shardReport(tree, 0, 2, "b"), shardReport(tree, 1, 2, "c")}, 2,
			"shard 1 measured against c and shard 0 against b"},
	}
	for _, tc := range cases {
		got := shardReportsProblem(tree, tc.reports, tc.shards)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s: problem = %q, want none", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: problem = %q, want one containing %q", tc.name, got, tc.want)
		}
	}
}

// writeShardReports writes one report file per shard for the tree at root
// and answers their paths.
func writeShardReports(t *testing.T, root string, reports ...RunnerReport) []string {
	t.Helper()
	tree, why := mutantsTreeID(root)
	if tree == "" {
		t.Fatalf("no tree id: %s", why)
	}
	dir := t.TempDir()
	var paths []string
	for i, r := range reports {
		if r.Tree == "" {
			r.Tree = tree
		}
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "shard-"+string(rune('a'+i))+".json")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

var survivorInGauge = MutantOutcome{File: "driveline/gauge.go", Line: 9, Col: 17, Mutation: "CONDITIONALS_BOUNDARY",
	Name: "driveline/gauge.go:9:17: CONDITIONALS_BOUNDARY", Status: "missed"}

// The merged shards are judged as one run: a survivor in ANY shard refuses,
// and a shard's caught mutants count in the total.
func TestJudgeShardReports_JudgesEveryShardsMutantsTogether(t *testing.T) {
	root, _ := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	caught := MutantOutcome{File: "torque/torque.go", Line: 5, Col: 16, Mutation: "ARITHMETIC_BASE", Status: "caught"}
	paths := writeShardReports(t, root,
		shardReport("", 0, 2, "base", caught),
		shardReport("", 1, 2, "base", survivorInGauge))
	out := filepath.Join(t.TempDir(), "merged.json")
	var log bytes.Buffer

	v, err := JudgeShardReports(root, MutantsConfig{AtMerge: true}, paths, 2, out, &log)

	if err != nil {
		t.Fatalf("JudgeShardReports: %v", err)
	}
	if !v.Refused || !strings.HasPrefix(v.Message, "driveline/gauge.go:9:17: CONDITIONALS_BOUNDARY\n") {
		t.Fatalf("a survivor in the second shard was not refused by name:\n%s", v.Message)
	}
	if v.Tested != 2 || v.Caught != 1 {
		t.Errorf("verdict = %+v, want both shards' 2 mutants counted, 1 caught", v)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("merged report not written: %v", err)
	}
	var merged RunnerReport
	if err := json.Unmarshal(data, &merged); err != nil || len(merged.Mutants) != 2 || merged.Shards != 0 || merged.Base != "base" {
		t.Errorf("merged report = %+v (err %v), want the whole lane's 2 mutants as one unsharded report on base", merged, err)
	}
}

func TestJudgeShardReports_PassesWhenEveryShardCaughtItsMutants(t *testing.T) {
	root, _ := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	caught := MutantOutcome{File: "torque/torque.go", Line: 5, Col: 16, Mutation: "ARITHMETIC_BASE", Status: "caught"}
	paths := writeShardReports(t, root, shardReport("", 0, 2, "base", caught), shardReport("", 1, 2, "base"))

	v, err := JudgeShardReports(root, MutantsConfig{AtMerge: true}, paths, 2, "", &bytes.Buffer{})

	if err != nil || v.Refused || v.Caught != 1 {
		t.Fatalf("verdict = %+v, err = %v, want one caught and no refusal:\n%s", v, err, v.Message)
	}
}

// A shard that never reported is not a shard with nothing to report.
func TestJudgeShardReports_RefusesAMissingShard(t *testing.T) {
	root, _ := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	paths := writeShardReports(t, root, shardReport("", 0, 2, "base"))

	v, err := JudgeShardReports(root, MutantsConfig{AtMerge: true}, paths, 2, "", &bytes.Buffer{})

	if err != nil || !v.Refused || !strings.Contains(v.Message, "1 shard report(s) arrived and 2 were expected") {
		t.Fatalf("verdict = %+v, err = %v, want a refusal naming the missing shard:\n%s", v, err, v.Message)
	}
}

func TestJudgeShardReports_RefusesAReportThatCannotBeRead(t *testing.T) {
	root, _ := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	missing := filepath.Join(t.TempDir(), "absent.json")

	v, err := JudgeShardReports(root, MutantsConfig{AtMerge: true}, []string{missing}, 1, "", &bytes.Buffer{})

	if err != nil || !v.Refused || !strings.Contains(v.Message, missing) {
		t.Fatalf("verdict = %+v, err = %v, want a refusal naming the unreadable report", v, err)
	}
}

func TestJudgeShardReports_RefusesReportsOfAnotherTree(t *testing.T) {
	root, _ := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	paths := writeShardReports(t, root, shardReport("not-this-tree", 0, 1, "base"))

	v, err := JudgeShardReports(root, MutantsConfig{AtMerge: true}, paths, 1, "", &bytes.Buffer{})

	if err != nil || !v.Refused || !strings.Contains(v.Message, "a different tree is different code") {
		t.Fatalf("verdict = %+v, err = %v, want a refusal: the reports measured other code", v, err)
	}
}

// Each shard's own run: it measures the files it owns, publishes them, and
// judges nothing.
func shardLane(t *testing.T) (root, base string) {
	t.Helper()
	root, base = laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	write(t, root, filepath.FromSlash("driveline/extra.go"), "package driveline\n\nfunc Extra() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "second changed file")
	return root, base
}

func readShardReport(t *testing.T, path string) RunnerReport {
	t.Helper()
	r, absent := readRunnerReport(path)
	if absent != "" {
		t.Fatalf("shard report: %s", absent)
	}
	return r
}

func TestMeasureLane_AShardExcludesTheFilesAnotherShardOwnsAndJudgesNothing(t *testing.T) {
	root, base := shardLane(t)
	calls := stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		mustWrite(t, gremlinsReportPath(root), livedInTorque)
		return 0, nil
	})
	out := filepath.Join(t.TempDir(), "shard-0.json")

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, ReportOut: out, Shard: 0, Shards: 2})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("a shard judged its own survivor instead of leaving it to the merged verdict:\n%s", v.Message)
	}
	if len(*calls) != 1 {
		t.Fatalf("gremlins ran %d time(s), want once", len(*calls))
	}
	// gauge.go adds 6 lines and extra.go 3, so shard 0 owns gauge.go alone.
	argv := strings.Join((*calls)[0].Argv, " ")
	if !strings.Contains(argv, `--exclude-files ^driveline/extra\.go$`) || strings.Contains(argv, "gauge") {
		t.Errorf("argv = %q, want it to exclude extra.go, which shard 1 owns, and nothing else", argv)
	}
	r := readShardReport(t, out)
	if r.Shard != 0 || r.Shards != 2 || len(r.Mutants) != 1 {
		t.Errorf("report = shard %d/%d with %d mutants, want shard 0/2 with the 1 measured", r.Shard, r.Shards, len(r.Mutants))
	}
}

func TestMeasureLane_TheOtherShardOwnsTheOtherFile(t *testing.T) {
	root, base := shardLane(t)
	calls := stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		mustWrite(t, gremlinsReportPath(root), `{"files":[]}`)
		return 0, nil
	})
	out := filepath.Join(t.TempDir(), "shard-1.json")

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, ReportOut: out, Shard: 1, Shards: 2}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	argv := strings.Join((*calls)[0].Argv, " ")
	if !strings.Contains(argv, `--exclude-files ^driveline/gauge\.go$`) || strings.Contains(argv, "extra") {
		t.Errorf("argv = %q, want it to exclude gauge.go, which shard 0 owns, and nothing else", argv)
	}
}

// A shard with no file measures nothing and still publishes its (empty)
// report: the aggregate counts reports, and a missing one is a refusal.
func TestMeasureLane_AShardThatOwnsNoFilePublishesAnEmptyReportWithoutRunningGremlins(t *testing.T) {
	root, base := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	calls := stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		t.Error("gremlins was started by a shard with nothing to measure")
		return 0, nil
	})
	out := filepath.Join(t.TempDir(), "shard-2.json")

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, ReportOut: out, Shard: 2, Shards: 3})

	if err != nil || v.Refused {
		t.Fatalf("verdict = %+v, err = %v, want a pass", v, err)
	}
	if len(*calls) != 0 {
		t.Errorf("gremlins ran %d time(s)", len(*calls))
	}
	if r := readShardReport(t, out); r.Shard != 2 || r.Shards != 3 || len(r.Mutants) != 0 {
		t.Errorf("report = %+v, want an empty report for shard 2/3", r)
	}
}

func TestMeasureLane_AShardOfALaneWithNoSourceChangePublishesAnEmptyReport(t *testing.T) {
	root, base := laneOverTorque(t, "docs/notes.txt", "hello\n")
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		t.Error("gremlins was started with no changed source file")
		return 0, nil
	})
	out := filepath.Join(t.TempDir(), "shard-0.json")

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, ReportOut: out, Shard: 0, Shards: 1})

	if err != nil || v.Refused {
		t.Fatalf("verdict = %+v, err = %v, want a pass", v, err)
	}
	if r := readShardReport(t, out); r.Shards != 1 || len(r.Mutants) != 0 {
		t.Errorf("report = %+v, want an empty report", r)
	}
}

func TestMeasureLane_AShardNeedsAReportAndAGoRepo(t *testing.T) {
	root, base := shardLane(t)

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Shard: 0, Shards: 2}); err == nil {
		t.Error("a shard with no --report was started; its outcomes would be lost")
	}
	cargo := t.TempDir()
	mustWrite(t, filepath.Join(cargo, "Cargo.toml"), "[workspace]\n")
	if _, err := MeasureLane(cargo, MutantsConfig{AtMerge: true}, MeasureOpts{Base: "x", ReportOut: "r.json", Shard: 0, Shards: 2}); err == nil {
		t.Error("a shard of a Cargo repo was started; only the Go half divides by file")
	}
}

// A shard that cannot read the diff cannot agree with the others on which files
// it owns, so it measures nothing.
func TestMeasureLane_AShardThatCannotReadTheDiffIsAnError(t *testing.T) {
	root, base := shardLane(t)
	prev := diffAddedLinesFn
	diffAddedLinesFn = func(string, string) (map[string]map[int]bool, error) { return nil, errors.New("git: gone") }
	t.Cleanup(func() { diffAddedLinesFn = prev })
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		t.Error("gremlins was started by a shard that could not divide the lane")
		return 0, nil
	})

	_, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, ReportOut: filepath.Join(t.TempDir(), "r.json"), Shard: 0, Shards: 2})

	if err == nil || !strings.Contains(err.Error(), "cannot agree with the others") {
		t.Errorf("error = %v, want the shard to say it cannot agree on which files it owns", err)
	}
}

// A shard that cannot publish is refused: without its report the aggregate
// could not tell it from a shard with nothing to report.
func TestFinishShard_ARefusalWhenTheReportCannotBeWritten(t *testing.T) {
	root, _ := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	blocked := filepath.Join(t.TempDir(), "file")
	mustWrite(t, blocked, "not a directory")

	v := finishShard(root, "base", MeasureOpts{ReportOut: filepath.Join(blocked, "r.json"), Shard: 1, Shards: 4}, nil, &bytes.Buffer{})

	if !v.Refused || !strings.Contains(v.Message, "shard 1/4 could not write its report") {
		t.Errorf("verdict = %+v, want a refusal naming the shard", v)
	}
}
