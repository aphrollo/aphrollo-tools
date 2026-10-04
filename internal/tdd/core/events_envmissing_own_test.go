package core

import (
	"testing"
	"time"
)

// A suite an interpreter without the repo's requirements could not collect is
// a run that proved nothing, at the edit hook and at the merge gate alike: the
// event log counts it as not tested, with its own cause.
func TestAppendGateLog_AnEnvironmentThatCouldNotRunTheSuiteIsANotTestedRun(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	isolateEvents(t)
	repo := eventsTestRepo(t)
	for _, verdict := range []string{"env-missing", "env-missing-rejected"} {
		AppendGateLog("premergecommit", repo, "python -m pytest -q", verdict, 3*time.Second)
	}

	got := ReadEvents(repo)
	if len(got) != 2 {
		t.Fatalf("%d events, want 2", len(got))
	}
	for _, e := range got {
		if e.Kind != "run.result" || e.Detail["result"] != "not-tested" || e.Detail["cause"] != "env-missing" {
			t.Errorf("%s: event = %+v, want a not-tested run.result with cause env-missing", e.Verdict, e)
		}
	}
}
