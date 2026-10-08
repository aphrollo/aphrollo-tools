package lock

import "testing"

// A caller sizing a budget asks the box how busy it is right now: one number,
// or that it cannot say. The probe is the one a timeout rejection already uses.
func TestBoxLoadPct_AnswersTheProbesLoad(t *testing.T) {
	t.Cleanup(SetMachineLoadSampleForTest(func(<-chan struct{}) (int, float64, []procSample, bool) {
		return 8, 37.5, nil, true
	}))

	pct, ok := BoxLoadPct()

	if !ok || pct != 37.5 {
		t.Fatalf("BoxLoadPct = %v, %v; want 37.5, true", pct, ok)
	}
}

func TestBoxLoadPct_AProbeThatCannotAnswerIsNotOk(t *testing.T) {
	t.Cleanup(SetMachineLoadSampleForTest(func(<-chan struct{}) (int, float64, []procSample, bool) {
		return 0, 0, nil, false
	}))

	if pct, ok := BoxLoadPct(); ok {
		t.Fatalf("BoxLoadPct = %v, true; want not ok when the probe has no reading", pct)
	}
}

// One sample at a time: a second asker while one is running declines, so a box
// already in trouble is not made to enumerate its processes twice over.
func TestBoxLoadPct_DeclinesWhileASampleIsInProgress(t *testing.T) {
	t.Cleanup(SetMachineLoadSampleForTest(func(<-chan struct{}) (int, float64, []procSample, bool) {
		return 8, 50, nil, true
	}))
	machineLoadMu.Lock()
	t.Cleanup(machineLoadMu.Unlock)

	if _, ok := BoxLoadPct(); ok {
		t.Fatal("BoxLoadPct answered while another sample held the guard")
	}
}
