package postedit

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/shadow"
)

// eventsOfKind is the events of root that have one kind, in order.
func eventsOfKind(root, kind string) []Event {
	var out []Event
	for _, e := range ReadEvents(root) {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// Each edit leaves one edit event naming the session that made it, so edits per
// message are counted from the log.
func TestPostEdit_EachEditWritesAnEditEventForItsSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")

	PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))
	PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	got := eventsOfKind(root, "edit")
	if len(got) != 2 || got[0].Actor != "sess-post" || got[1].Actor != "sess-post" || got[0].Detail["edit"] == "" || got[0].Detail["edit"] == got[1].Detail["edit"] {
		t.Fatalf("edit events = %+v, want two, each by sess-post and naming its own ledger record", got)
	}
}

func TestPostEdit_ASettledRunWritesARunResultWithTheEditToVerdictLatency(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := mkProject(t, "go.mod")

	PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), fakeRun(false, "--- FAIL: TestThing\n want 1"))

	var settled []Event
	for _, e := range eventsOfKind(root, "run.result") {
		if e.Detail["latency_ms"] != "" {
			settled = append(settled, e)
		}
	}
	if len(settled) != 1 {
		t.Fatalf("%d settled run.result events, want 1: %+v", len(settled), settled)
	}
	ms, err := strconv.ParseInt(settled[0].Detail["latency_ms"], 10, 64)
	if err != nil || ms < 0 || settled[0].Detail["result"] != "red" {
		t.Fatalf("event = %+v, want a red result with a latency in milliseconds", settled[0])
	}
}

func TestPostEdit_ATimedOutRunIsRecordedAsNotTestedWithItsCause(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := mkProject(t, "go.mod")
	invoked := 0

	PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), countingTimeoutRun(&invoked))

	var notTested []Event
	for _, e := range eventsOfKind(root, "run.result") {
		if e.Detail["result"] == "not-tested" {
			notTested = append(notTested, e)
		}
	}
	if len(notTested) != 1 || notTested[0].Detail["cause"] != "timeout" {
		t.Fatalf("not-tested events = %+v, want one with cause timeout", notTested)
	}
}

// harvestRun saves a finished run phase with the given log and exit code, harvests
// it the way the next hook does, and answers the line it printed.
func harvestRun(t *testing.T, root, session, log string, exit int, tree string) string {
	t.Helper()
	saveDeferredJob(DeferredJob{
		Project: root, Phase: "run", Dir: root, PID: 99, Runner: []string{"go", "test", "./..."},
		Started: time.Now(), Session: session, File: filepath.Join(root, "widget.go"),
	})
	job, ok := loadDeferredJob(session, root)
	if !ok {
		t.Fatal("setup: the job did not load back")
	}
	mustWrite(t, job.Log, log)
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: exit, Seconds: 1, TreeKey: tree})
	line, ok := WaitDeferredEditJob(root)
	if !ok {
		t.Fatal("the harvest found no job")
	}
	return line
}

// ratchet: test_removed TestHarvest_AnAgreeingRunWritesNoShadowEvent: an agreeing run is written now, proved by TestHarvest_AnAgreeingRunWritesACompactShadowEvent

// A run both sides read alike is written too, so the fold counts shadow events
// alone: agreement is the share of those that agree.
func TestHarvest_AnAgreeingRunWritesACompactShadowEvent(t *testing.T) {
	shadow.Flush() // another test's queued run is not this one's
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := mkProject(t, "go.mod")

	line := harvestRun(t, root, "s-agree", "--- FAIL: TestWidget (0.00s)\n    widget_test.go:9: want 1\nFAIL\n", 1, "tree-red")
	if !strings.Contains(line, "red") {
		t.Fatalf("harvest = %q, want the red line", line)
	}
	shadow.Flush()
	got := kashadowRunEvents(root)
	if len(got) != 1 {
		t.Fatalf("an agreeing red wrote %d shadow events, want 1: %+v", len(got), got)
	}
	if d := got[0].Detail; d["relation"] != "agree" || d["rule"] != "run-verdict" || d["hook"] != "posttooluse-run" || d["key"] != "tree-red" || got[0].Cmd != "" {
		t.Errorf("shadow detail = %v, want an agreeing run-verdict under tree-red with no command", d)
	}
}

// A run the hook ran to its end in the foreground is shadowed like a harvested one.
func TestPostEdit_AForegroundRunIsShadowedAfterTheAnswer(t *testing.T) {
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := mkProject(t, "go.mod")

	PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), fakeRun(true, "ok\nPASS"))
	if n := len(eventsOfKind(root, "shadow")); n != 0 {
		t.Fatalf("%d shadow events written before the flush, want none", n)
	}
	shadow.Flush()
	got := kashadowRunEvents(root)
	if len(got) != 1 || got[0].Detail["relation"] != "agree" || got[0].Detail["hook"] != "posttooluse-run" {
		t.Errorf("shadow events = %+v, want one agreeing run record", got)
	}
}

// phaseVerdict reads every failing run as a red and cannot say bogus. A run the
// gate calls red-bogus must not be filed as a mismatch of that reading: it is
// not comparable, and says both classes. The record is queued while the hook
// builds its answer and written only by the flush after the answer is out, and
// carries no command and the tree the run judged.
// ratchet: test_removed TestHarvest_ARunVerdictIsShadowedAfterTheAnswer: its ordering check moved here, since an agreeing run now writes no event
func TestHarvest_ABogusRedIsNotComparableNeverAMismatch(t *testing.T) {
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := mkProject(t, "go.mod")

	if line := harvestRun(t, root, "s-bogus", "ImportError: cannot import name widget\nFAIL\n", 1, "tree-bogus"); !strings.Contains(line, "bogus") {
		t.Fatalf("harvest = %q, want the gate's red-bogus line", line)
	}
	if n := len(eventsOfKind(root, "shadow")); n != 0 {
		t.Fatalf("%d shadow events written before the answer was out, want none until the flush", n)
	}
	shadow.Flush()

	got := kashadowRunEvents(root)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	d := got[0].Detail
	if d["relation"] != "not-comparable" || d["aphrollo"] != "red-bogus" || d["aphrollo_verdict"] != "red-bogus" || d["trellis_verdict"] != "red" || d["key"] != "tree-bogus" {
		t.Errorf("shadow detail = %v, want not-comparable: aphrollo red-bogus where the kernel's input can only say red, under tree-bogus", d)
	}
	if got[0].Cmd != "" {
		t.Errorf("a shadow event carries no command: %+v", got[0])
	}
}

// The record reuses the result the harvest read: shadowing a run costs the hook
// no read of the phase's log that it would not make anyway.
func TestHarvest_ShadowingARunReadsThePhaseLogNoMoreOften(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	reads := 0
	old := readPhaseLog
	readPhaseLog = func(name string) ([]byte, error) { reads++; return old(name) }
	t.Cleanup(func() { readPhaseLog = old })
	oldEnabled := shadow.Enabled
	t.Cleanup(func() { shadow.Enabled = oldEnabled })

	count := func(enabled bool, session string) int {
		shadow.Enabled = enabled
		reads = 0
		root := mkProject(t, "go.mod")
		harvestRun(t, root, session, "--- FAIL: TestWidget (0.00s)\nFAIL\n", 1, "tree-"+session)
		shadow.Flush()
		return reads
	}
	off, on := count(false, "s-off"), count(true, "s-on")
	if off == 0 || on != off {
		t.Errorf("the harvest read the phase log %d times with shadowing off and %d times with it on, want the same non-zero count", off, on)
	}
}

// kashadowRunEvents are the shadow events that compare a run's verdict, which is
// what these tests are about: a run is also folded into its lane's record, and a
// fold that cannot be made (a test project has no branch to be a lane) leaves an
// unjudged lane-fold event of its own beside them.
func kashadowRunEvents(root string) []Event {
	var out []Event
	for _, e := range eventsOfKind(root, "shadow") {
		if e.Detail["rule"] == "run-verdict" {
			out = append(out, e)
		}
	}
	return out
}
