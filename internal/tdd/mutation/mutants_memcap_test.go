package mutation

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// mutantsCapHelperEnv marks the re-executed test binary as the allocating
// child the cap tests start.
const mutantsCapHelperEnv = "MUTATIONTEST_CAP_HELPER_MB"

// TestMutantsCapHelper_Allocate is not a test: it is the runaway mutant the
// cap tests start. It touches mutantsCapHelperEnv megabytes and then waits
// (bounded) to be ended.
func TestMutantsCapHelper_Allocate(t *testing.T) {
	raw := os.Getenv(mutantsCapHelperEnv)
	if raw == "" {
		t.Skip("child process of the memory-cap tests")
	}
	mb, _ := strconv.Atoi(raw)
	b := make([]byte, mb<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	time.Sleep(20 * time.Second)
	_ = b[0]
}

func runCappedMutantsHelper(t *testing.T, c MemCap) (int, string) {
	t.Helper()
	defer SetMemCapForTest(c)()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var log bytes.Buffer
	env := append(os.Environ(), mutantsCapHelperEnv+"=600")
	code, err := runMutantsTool(ctx, t.TempDir(), env, []string{os.Args[0], "-test.run=^TestMutantsCapHelper_Allocate$"}, &log)
	if err != nil {
		t.Fatalf("runMutantsTool: %v", err)
	}
	return code, log.String()
}

// A mutation run's runaway is one mutant's test process. Ending it at the cap
// leaves the run going and says in the log why that mutant died, so a reader
// can tell a killed-by-crash mutant from a test that failed.
func TestRunMutantsTool_CapEndsARunawayWorkerAndNamesTheReason(t *testing.T) {
	code, log := runCappedMutantsHelper(t, MemCap{MB: 150, Why: "test", KillLargest: true})
	if code == 0 {
		t.Fatal("a worker ended at the cap must not report success")
	}
	if !strings.Contains(log, "killed-by-crash") || !strings.Contains(log, "0.1 GB") {
		t.Fatalf("log = %q, want the killed-by-crash reason naming the cap", log)
	}
	if strings.Contains(log, "the run reached no verdict") {
		t.Fatalf("log = %q: one worker's death must not be reported as the whole run's", log)
	}
}

// Where the whole tree is ended at the cap, the run reached no verdict and
// the log says OOM-KILLED at the cap, never a timeout.
func TestRunMutantsTool_CapEndingTheWholeRunReadsAsOOMKilled(t *testing.T) {
	_, log := runCappedMutantsHelper(t, MemCap{MB: 150, Why: "test"})
	if !strings.Contains(log, "OOM-KILLED at 0.1 GB") || !strings.Contains(log, "the run reached no verdict") {
		t.Fatalf("log = %q, want OOM-KILLED at the cap and no verdict", log)
	}
	if strings.Contains(strings.ToUpper(log), "TIMED OUT") {
		t.Fatalf("log = %q must not read as a timeout", log)
	}
}

func TestReportCapKills_SaysNothingForACleanRun(t *testing.T) {
	var log bytes.Buffer
	reportCapKills(&log, CapResult{Cap: MemCap{MB: 1024}})
	if log.Len() != 0 {
		t.Fatalf("log = %q, want silence when the cap ended nothing", log.String())
	}
}

// A measurement never starts on a box with no memory to give it: it is the
// same unavailable verdict a busy CI runner gets, with the reason, and no
// build or mutation tool is launched.
func TestMeasure_NoHeadroomIsUnavailableWithTheReasonAndStartsNothing(t *testing.T) {
	root, base := laneOverTorque(t, "driveline/gauge.go", gaugeSource)
	defer SetMemBoxForTest(MemBox{RAMMB: 32768, AvailMB: 500})()
	prev := mutantsHeadroomWait
	mutantsHeadroomWait = 0
	t.Cleanup(func() { mutantsHeadroomWait = prev })
	started := false
	defer SetMutantsExecForTest(func(context.Context, string, []string, []string, io.Writer) (int, error) {
		started = true
		return 0, nil
	})()

	var log bytes.Buffer
	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !strings.HasPrefix(v.Unavailable, "SKIPPED — memory headroom: 0.5 GB available") || v.Refused {
		t.Fatalf("verdict = %+v, want unavailable (not refused) naming 0.5 GB available", v)
	}
	if started {
		t.Fatal("a mutation tool was started on a box with no headroom")
	}
	if !strings.Contains(log.String(), "not measuring here") {
		t.Fatalf("log = %q, want the refusal said in the run's log", log.String())
	}
}

func TestCapShare_ALoneRunHoldsThePoolAndShardsSplitIt(t *testing.T) {
	if got := capShare(context.Background()); got != 1 {
		t.Errorf("capShare of a bare context = %d, want 1", got)
	}
	if got := capShare(withCapShare(context.Background(), 6)); got != 6 {
		t.Errorf("capShare after withCapShare(6) = %d, want 6", got)
	}
	if got := capShare(withCapShare(context.Background(), 0)); got != 1 {
		t.Errorf("capShare after withCapShare(0) = %d, want 1: zero shards is not a division", got)
	}
}
