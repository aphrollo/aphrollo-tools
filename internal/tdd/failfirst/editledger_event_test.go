package failfirst

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func runResultEvents(root string) []Event {
	var out []Event
	for _, e := range ReadEvents(root) {
		if e.Kind == "run.result" {
			out = append(out, e)
		}
	}
	return out
}

// An edit id is its time, so the verdict can say how long the agent waited
// for it: the delay between the edit and the verdict being recorded.
func TestEditLatency_IsTheTimeSinceTheEditTheIDNames(t *testing.T) {
	made := time.Unix(1_700_000_000, 123_000_000)
	id := strconv.FormatInt(made.UnixNano(), 36)

	got, ok := editLatency(id, made.Add(1500*time.Millisecond))

	if !ok || got != 1500*time.Millisecond {
		t.Fatalf("editLatency = %v, %v, want 1.5s", got, ok)
	}
}

func TestEditLatency_AnIDThatIsNoTimeHasNoLatency(t *testing.T) {
	if got, ok := editLatency("not a time!", time.Now()); ok {
		t.Fatalf("editLatency of a malformed id = %v, true, want none", got)
	}
}

func TestRecordEditVerdict_WritesARunResultEventWithTheOutcomeAndLatency(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n")
	id := recordEdit(root, filepath.Join(root, "src/widget.rs"))

	recordEditVerdict(root, id, "cargo test --lib", Red, "test widget::tests::a ... FAILED\n")

	got := runResultEvents(root)
	if len(got) != 1 {
		t.Fatalf("%d run.result events, want 1: %+v", len(got), got)
	}
	e := got[0]
	ms, err := strconv.ParseInt(e.Detail["latency_ms"], 10, 64)
	if e.Verdict != "red" || e.Detail["result"] != "red" || e.Detail["edit"] != id || err != nil || ms < 0 {
		t.Fatalf("event = %+v, want a red result for edit %s with a latency in milliseconds", e, id)
	}
}

// One run that settles several edits is one result per edit: the latency is
// each edit's own.
func TestRecordEditVerdict_AJoinedRunWritesOneRunResultPerEdit(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n")
	write(t, root, "src/other.rs", "pub fn other() -> i32 { 3 }\n")
	joined := recordEdits(root, []string{filepath.Join(root, "src/widget.rs"), filepath.Join(root, "src/other.rs")})

	recordEditVerdict(root, joined, "cargo test --lib", Green, "test widget::tests::a ... ok\n")

	if got := runResultEvents(root); len(got) != 2 {
		t.Fatalf("%d run.result events, want one per edit (2): %+v", len(got), got)
	}
}

func TestRecordEditVerdict_NoEditNoRunResult(t *testing.T) {
	root := ledgerRepo(t)

	recordEditVerdict(root, "", "cargo test --lib", Green, "")

	if got := runResultEvents(root); len(got) != 0 {
		t.Fatalf("an empty edit id wrote %+v", got)
	}
}
