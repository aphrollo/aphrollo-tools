package cli

import (
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// stopHookEvents maps the gate verb each turn-end hook runs to the event it
// answers.
var stopHookEvents = map[string]tdd.StopEvent{
	"stop":          tdd.StopHookStop,
	"subagentstop":  tdd.StopHookSubagentStop,
	"taskcompleted": tdd.StopHookTaskCompleted,
}

// runStopCheck answers Stop, SubagentStop and TaskCompleted from the payload
// the harness piped in. It writes what the harness reads for the verdict
// (stdout for a stop, stderr and exit 2 for a task) and nothing for an allow;
// a payload it cannot read allows.
func runStopCheck(verb string, raw []byte, stdout, stderr io.Writer) int {
	event := stopHookEvents[verb]
	verdict := tdd.DecideStop(event, raw)
	out, errOut, code := tdd.RenderStopVerdict(event, verdict)
	stdout.Write(out)
	stderr.Write(errOut)
	// After the answer is written: the shadow record never changes it. Only a Stop
	// or SubagentStop that blocked on an unseen red is a fact the kernel's stop-red
	// rule is asked about; an allow has no red for it to read.
	if verdict.Red && event != tdd.StopHookTaskCompleted {
		recordStopShadow(verb, raw)
	}
	return code
}

// recordStopShadow records what the kernel's stop-red rule would have decided
// beside the block the live check just made, inside the shadow budget.
func recordStopShadow(verb string, raw []byte) {
	src, ok := hookSource(raw)
	pl, pok := shadow.ParsePayload(raw)
	if !ok || !pok {
		return
	}
	shadow.RecordStop(tdd.ShadowWorld(), verb, src, pl, shadow.StopFacts{Unseen: true, Blocked: true})
}
