package tdd

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A measurement that overlapped two PRs' CI jobs on this box reported
// `Killed: 14, Lived: 0 ... Timed out: 21`; the same tree measured on a quiet
// box caught 35 of 35. gremlins derives each mutant's timeout from its
// coverage run, so load that arrives with a CI job turns healthy mutants into
// timeouts and the gate refuses them as unmeasured. The run therefore does
// not start while this box's runner jobs are busy: it waits for them and says
// so.
func TestMeasure_GoLaneWaitsForBusyCIRunnerJobsBeforeRunning(t *testing.T) {
	root, base := measurableTorqueLane(t)
	t.Cleanup(setCIRunnerWaitForTest(time.Millisecond, time.Minute))
	probes := 0
	t.Cleanup(SetCIRunnerJobsForTest(func() []int {
		probes++
		if probes <= 2 {
			return []int{4242}
		}
		return nil
	}))
	probesAtRun := -1
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		probesAtRun = probes
		mustWrite(t, gremlinsReportPath(root), livedInTorque)
		return 0, nil
	})
	var log bytes.Buffer

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if probesAtRun != 3 {
		t.Errorf("gremlins started after %d probe(s), want 3 — two that found the runner job busy, one that found it gone",
			probesAtRun)
	}
	if !strings.Contains(log.String(), "4242") {
		t.Errorf("log = %q, want the wait said out loud, naming the busy runner job", log.String())
	}
}

// The wait is bounded: a runner that never goes idle must not hold a merge
// forever. Past the bound the run goes ahead and says why its timeouts may
// not mean hangs.
func TestMeasure_GoLaneMeasuresAnywayOnceTheCIWaitRunsOut(t *testing.T) {
	root, base := measurableTorqueLane(t)
	t.Cleanup(setCIRunnerWaitForTest(time.Millisecond, 20*time.Millisecond))
	t.Cleanup(SetCIRunnerJobsForTest(func() []int { return []int{4242, 4343} }))
	ran := false
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		ran = true
		mustWrite(t, gremlinsReportPath(root), livedInTorque)
		return 0, nil
	})
	var log bytes.Buffer

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if !ran {
		t.Fatalf("gremlins never ran — a runner that never goes idle held the measurement past its bound")
	}
	if !strings.Contains(log.String(), "measuring anyway") {
		t.Errorf("log = %q, want the run to say it went ahead with runner jobs still busy", log.String())
	}
}

// A box with no runner jobs, which is every box without runners, measures
// exactly as it did before the wait existed: at once, and with nothing new
// in its log.
func TestMeasure_GoLaneWithNoCIRunnerJobsNeitherWaitsNorSaysSo(t *testing.T) {
	root, base := measurableTorqueLane(t)
	t.Cleanup(setCIRunnerWaitForTest(time.Millisecond, 20*time.Millisecond))
	probes := 0
	t.Cleanup(SetCIRunnerJobsForTest(func() []int {
		probes++
		return nil
	}))
	stubGremlinsReport(t, root, livedInTorque)
	var log bytes.Buffer

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if probes != 1 {
		t.Errorf("probed for runner jobs %d time(s), want once — an idle box has nothing to poll for", probes)
	}
	if strings.Contains(log.String(), "runner") {
		t.Errorf("log = %q, want no word about CI runners on a box with none busy", log.String())
	}
}

// What counts as a busy runner job, read from a /proc laid out the way this
// box's is: the actions runner's Runner.Worker, one per running job, beside
// its always-running Runner.Listener. A shell whose command line merely
// mentions the name is not a job, and neither is the job this process runs
// inside — the nightly mutants workflow measures from within one.
func TestBusyCIRunnerJobs_CountsOtherWorkersOnly(t *testing.T) {
	proc := t.TempDir()
	fakeProc := func(pid, ppid int, argv ...string) {
		dir := filepath.Join(proc, strconv.Itoa(pid))
		mustWrite(t, filepath.Join(dir, "cmdline"), strings.Join(argv, "\x00")+"\x00")
		mustWrite(t, filepath.Join(dir, "stat"), fmt.Sprintf("%d (odd) name) S %d 0 0\n", pid, ppid))
	}
	fakeProc(10, 1, "/opt/actions-runner-a/bin/Runner.Listener", "run")
	fakeProc(11, 10, "/opt/actions-runner-a/bin.2.337.0/Runner.Worker", "spawnclient", "1", "2")
	fakeProc(20, 1, "/opt/actions-runner-b/bin/Runner.Listener", "run")
	fakeProc(21, 20, "/opt/actions-runner-b/bin.2.337.0/Runner.Worker", "spawnclient", "3", "4")
	fakeProc(22, 21, "/bin/bash", "-e", "/home/runner/_work/_temp/step.sh")
	fakeProc(23, 22, "/usr/local/bin/aphrollo", "gate", "mutants", "run")
	fakeProc(30, 1, "/bin/bash", "-c", "ps aux | grep Runner.Worker")
	mustWrite(t, filepath.Join(proc, "self"), "not a pid")

	got := busyCIRunnerJobs(proc, 23)

	if !reflect.DeepEqual(got, []int{11}) {
		t.Errorf("busy runner jobs = %v, want [11] — not the listeners, not the grep, not the job pid 23 runs inside", got)
	}
}
