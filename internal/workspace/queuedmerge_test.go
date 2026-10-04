package workspace

import (
	"bytes"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A PR the verb queued and nobody waited for is merged by GitHub later. Local
// trunk taking the merge in is the first the log can see of it: it is recorded
// as the merge the verb queued (the lane's, no escape), not as an outside one,
// so the lane's speed ends at the merge.
func TestSync_ARecordedQueuedPRThatTrunkTookInIsMergedOnItsLaneNotOutside(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Lane: "lane/q", Verdict: "queued",
		Detail: map[string]string{"pr": "1200", "method": "merge queue"}})
	sha := landOnOrigin(t, clone, "Queue the thing (#1200)")

	for range 2 { // a second pass records nothing more
		if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	if outside := outsideMerges(t); len(outside) != 0 {
		t.Fatalf("outside merge events = %+v, want none for a queued PR", outside)
	}
	if escapes := ofKind(emitted(t), "escape"); len(escapes) != 0 {
		t.Fatalf("escape events = %+v, want none for a queued PR", escapes)
	}
	var oks []tdd.Event
	for _, e := range ofKind(emitted(t), "merge") {
		if e.Verdict == "ok" {
			oks = append(oks, e)
		}
	}
	if len(oks) != 1 || oks[0].Lane != "lane/q" || oks[0].Detail["pr"] != "1200" || oks[0].Detail["sha"] != sha || oks[0].Detail["method"] != "merge queue" {
		t.Fatalf("merge ok events = %+v, want one for lane/q PR 1200 at %s by merge queue", oks, sha)
	}
}
