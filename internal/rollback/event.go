package rollback

import core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"

// EventKind is the kind of every record this package writes to events.jsonl.
const EventKind = "update"

// Emit appends one update event: stage is what happened (swap, pin, unpin),
// verdict how it ended. An update moves the box's binary, not a repo's, so the
// record names no repo or lane.
func Emit(stage, verdict string, detail map[string]string) {
	core.AppendEvent(core.Event{Kind: EventKind, Stage: stage, Verdict: verdict, Detail: detail})
}
