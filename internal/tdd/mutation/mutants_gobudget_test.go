package mutation

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// A Go lane was budgeted with Cargo's figures. With 12 GB free the box
// printed `refused — 12 GB free, one job needs 15.0 GB (5.8 MB source-tree
// copy + 15.0 GB build dir (estimated, never built))` and measured nothing,
// though gremlins keeps no per-job build dir: it builds through the shared
// GOCACHE, and a job puts only its copy of the module and that package's
// test binaries on the drive. Two such jobs fit in the 2 GB left above the
// reserve many times over.
func TestMeasure_GoLaneIsBudgetedWithGoFiguresNotACargoBuildDir(t *testing.T) {
	root, base := measurableTorqueLane(t)
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	t.Cleanup(SetFreeSpaceForTest(12, true))
	stubGremlinsReport(t, root, livedInTorque)
	var log bytes.Buffer

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused && strings.Contains(v.Message, "GB free") {
		t.Fatalf("the Go lane was refused on disk with 12 GB free:\n%s", v.Message)
	}
	if !strings.Contains(log.String(), "fits all 2 jobs") {
		t.Errorf("log = %q, want both jobs admitted", log.String())
	}
	if strings.Contains(log.String(), "never built") {
		t.Errorf("log = %q, want no Cargo build dir named for a runner that keeps none", log.String())
	}
}

// The worker count divided free memory by Cargo's 8 GB per shard as well:
// `free 21GB/8=2`. A gremlins worker is one `go test` of one package, and a
// whole two-worker run of this repo's largest package peaked at 645 MB of
// process tree, so memory must not be what holds a 24-core box with 21 GB
// free to two workers. The cores (24/3=8) and the cap of 8 decide it.
func TestMeasure_GoLaneWorkerCountIsNotHeldByCargosMemoryPerShard(t *testing.T) {
	root, base := measurableTorqueLane(t)
	// The fixture pins the worker count, and this test is about deriving it.
	pinned := mutantsJobsForThisBoxFn
	mutantsJobsForThisBoxFn = mutantsJobsForThisBox
	t.Cleanup(func() { mutantsJobsForThisBoxFn = pinned })
	t.Cleanup(setMutantsBoxForTest(24, 63, 21))
	var argv []string
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		argv = c.Argv
		mustWrite(t, gremlinsReportPath(root), livedInTorque)
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if got := flagValue(argv, "--workers"); got != "8" {
		t.Errorf("gremlins --workers = %q, want 8 (min(cores 24/3=8, cap 8); 21 GB free is no limit for Go workers)", got)
	}
}
