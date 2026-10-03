package postedit

import (
	"path/filepath"
	"strconv"
	"testing"
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
