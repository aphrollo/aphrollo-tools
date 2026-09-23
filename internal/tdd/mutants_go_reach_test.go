package tdd

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #695, the measurement's half of #691's class. gremlins judges a
// mutant with the MUTATED PACKAGE's own tests. A mutant that only an
// importing package's test kills is therefore reported Lived, and the
// pre-merge gate refuses the merge naming it as a survivor — a confident
// wrong answer in the direction that blocks correct work, over evidence that
// does not exist.
//
// Measured on a three-package module, one variable changed between runs:
// killing test in the importer, Killed 0 Lived 1; killing test removed
// entirely, Killed 0 Lived 1; killing test moved into the mutated package,
// Killed 1 Lived 0. The test's PRESENCE made no difference, only its
// LOCATION — so the Lived verdict is not about what the tests constrain.
//
// The selection is inside gremlins, so it cannot be widened from here the
// way #691/#694 widened a selection this repo builds. What can be fixed is
// the CLAIM: where a package outside the mutated one has tests that reach
// the mutated code, gremlins' run could not have observed the killing test,
// and "nothing in this selection killed it" is not "nothing kills it". That
// mutant is INCONCLUSIVE — the same verdict NoTestsSelected, BuildOnly and
// the Go proof's SCOPE UNKNOWN already give an untested one — and never a
// survivor.
//
// The module these run over is the fixture from the prove arm's own
// #691 test (torqueAndItsImporter): the mutated function in package torque,
// whose own test does not constrain the mutated line, and package driveline,
// an importer whose test does. The reach behind the classification is a REAL
// `go list` over that module; only the gremlins report is a fixture, because
// gremlins reports 0.00% mutator coverage on Windows and cannot be run here
// at all.

// measurableTorqueLane is that module as a lane to measure: the base is the
// commit before the three packages landed, so the run has changed Go source
// to scope itself to.
func measurableTorqueLane(t *testing.T) (root, base string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	t.Cleanup(SetMutantsGOOSForTest("linux"))
	t.Cleanup(setMutantsJobsForTest(1, "pinned"))
	root, _ = torqueAndItsImporter(t)
	return root, strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD~1"))
}

// stubGremlinsReport makes the one gremlins run write the given report and
// exit 0, so the measurement judges exactly the outcomes named here.
func stubGremlinsReport(t *testing.T, root, report string) {
	t.Helper()
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		mustWrite(t, gremlinsReportPath(root), report)
		return 0, nil
	})
}

// livedInTorque is gremlins' report for the measured defect: the halving in
// torque.Split reported Lived, at the position the prove arm mutates.
const livedInTorque = `{"files":[{"file_name":"torque/torque.go","mutations":[
	{"type":"ARITHMETIC_BASE","status":"LIVED","line":5,"column":16}]}]}`

// The defect: a mutant the module's tests DO kill, refused at the merge as a
// survivor because gremlins judged it with the mutated package's own tests.
func TestMeasure_GoMutantAnImporterKillsIsInconclusiveRatherThanASurvivor(t *testing.T) {
	root, base := measurableTorqueLane(t)
	stubGremlinsReport(t, root, livedInTorque)

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("the merge was refused over a mutant driveline's test kills, which gremlins never ran:\n%s", v.Message)
	}
	if len(v.Unaccepted) != 0 {
		t.Fatalf("named %d unaccepted survivor(s) out of a selection that could not have killed them: %+v",
			len(v.Unaccepted), v.Unaccepted)
	}
	if !strings.Contains(v.Message, "mutant SCOPE UNKNOWN") {
		t.Errorf("message = %q, want the same inconclusive vocabulary the Go proof arm uses", v.Message)
	}
	if !strings.Contains(v.Message, "driveline") {
		t.Errorf("message = %q, want the package whose tests reach the mutant named", v.Message)
	}
}

// The half that must not regress: nothing outside the mutated package has
// tests that reach it, so its own tests WERE the whole opportunity, the Lived
// verdict is a real survivor, and the merge is still refused by name.
// driveline is the top of this module — no package imports it — so the
// inconclusive verdict has nothing to stand on.
func TestMeasure_GoMutantNothingOutsideItsPackageReachesIsStillRefusedByName(t *testing.T) {
	root, base := measurableTorqueLane(t)
	stubGremlinsReport(t, root, `{"files":[{"file_name":"driveline/driveline.go","mutations":[
		{"type":"ARITHMETIC_BASE","status":"LIVED","line":8,"column":9}]}]}`)

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("a genuine survivor stopped blocking:\n%s", v.Message)
	}
	if v.Missed != 1 || len(v.Inconclusive) != 0 {
		t.Fatalf("verdict = %+v, want the one survivor counted as missed and nothing called inconclusive", v)
	}
	if !strings.Contains(v.Message, "driveline/driveline.go:8:9: ARITHMETIC_BASE") {
		t.Errorf("message = %q, want the survivor named", v.Message)
	}
}

// The other way a survivor stays a survivor, and the sharper one: a package
// outside the mutated one DOES reach it but carries no test file at all. It
// could never have killed anything, so counting it would turn every survivor
// in an imported package into an inconclusive one — the gate hole this fix
// must not open.
func TestMeasure_GoMutantOnlyATestlessImporterReachesIsStillRefusedByName(t *testing.T) {
	root, base := testlessImporterLane(t)
	stubGremlinsReport(t, root, `{"files":[{"file_name":"hub/hub.go","mutations":[
		{"type":"ARITHMETIC_BASE","status":"LIVED","line":5,"column":16}]}]}`)

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("a survivor whose only importer has no test at all stopped blocking:\n%s", v.Message)
	}
	if v.Missed != 1 || len(v.Inconclusive) != 0 {
		t.Fatalf("verdict = %+v, want the one survivor counted as missed and nothing called inconclusive", v)
	}
}

// testlessImporterLane is a module whose mutated package IS imported from
// outside — by a package with no _test.go of its own, which can therefore
// kill nothing.
func testlessImporterLane(t *testing.T) (root, base string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	t.Cleanup(SetMutantsGOOSForTest("linux"))
	t.Cleanup(setMutantsJobsForTest(1, "pinned"))
	root = makeGoRepo(t)
	base = strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD"))
	write(t, root, filepath.FromSlash("hub/hub.go"),
		"package hub\n\n// Share is one wheel's half of the axle's torque.\n"+
			"func Share(input float64) float64 {\n\treturn input / 2.0\n}\n")
	write(t, root, filepath.FromSlash("hub/hub_test.go"),
		"package hub\n\nimport \"testing\"\n\n"+
			"func TestShareIsPositiveForAPositiveInput(t *testing.T) {\n"+
			"\tif Share(400.0) <= 0 {\n\t\tt.Fatal(\"not positive\")\n\t}\n}\n")
	write(t, root, filepath.FromSlash("axle/axle.go"),
		"package axle\n\nimport \"example.com/m/hub\"\n\n"+
			"// Left is the near-side wheel's torque.\nfunc Left(input float64) float64 {\n"+
			"\treturn hub.Share(input)\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	return root, base
}

// A reach nobody could read is not an empty reach. "Nothing else reaches this
// package" and "I could not find out" are opposite claims, and only the first
// could ever ground a survivor — so a graph that fails to load leaves the
// survivor inconclusive, carrying the failure's own words.
func TestMeasure_GoSurvivorWhoseReachCannotBeReadIsInconclusiveAndSaysWhy(t *testing.T) {
	root, base := measurableTorqueLane(t)
	stubGremlinsReport(t, root, livedInTorque)
	setGoReachGraphForTest(t, func(string) (goReachGraph, error) {
		return goReachGraph{}, errors.New("go list in /repo: exit status 1: go.mod names no module")
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("a survivor claim was made out of a reach nobody could read:\n%s", v.Message)
	}
	if len(v.Inconclusive) != 1 {
		t.Fatalf("verdict = %+v, want the one survivor left inconclusive", v)
	}
	if !strings.Contains(v.Message, "go.mod names no module") {
		t.Errorf("message = %q, want the reader's own words for why the reach is unknown", v.Message)
	}
}

// The cost of the classification is one `go list` for the whole measured set,
// not one per surviving mutant — and none at all for a run with nothing to
// classify.
func TestMeasure_GoReachGraphIsReadOncePerRunAndOnlyWhenSomethingSurvived(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report string
		want   int
	}{
		{"three survivors across two packages", `{"files":[
			{"file_name":"torque/torque.go","mutations":[
				{"type":"ARITHMETIC_BASE","status":"LIVED","line":5,"column":16},
				{"type":"ARITHMETIC_BASE","status":"LIVED","line":6,"column":9}]},
			{"file_name":"driveline/driveline.go","mutations":[
				{"type":"ARITHMETIC_BASE","status":"LIVED","line":8,"column":9}]}]}`, 1},
		{"nothing survived", `{"files":[{"file_name":"torque/torque.go","mutations":[
			{"type":"ARITHMETIC_BASE","status":"KILLED","line":5,"column":16}]}]}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, base := measurableTorqueLane(t)
			stubGremlinsReport(t, root, tc.report)
			reads := 0
			setGoReachGraphForTest(t, func(r string) (goReachGraph, error) {
				reads++
				return loadGoReachGraph(r)
			})

			if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base}); err != nil {
				t.Fatalf("MeasureLane: %v", err)
			}

			if reads != tc.want {
				t.Errorf("read the module's reach graph %d time(s), want %d", reads, tc.want)
			}
		})
	}
}

// setGoReachGraphForTest states the module graph — or a failure to read one —
// for the duration of a test.
func setGoReachGraphForTest(t *testing.T, fn func(root string) (goReachGraph, error)) {
	t.Helper()
	prev := goReachGraphFn
	goReachGraphFn = fn
	t.Cleanup(func() { goReachGraphFn = prev })
}
