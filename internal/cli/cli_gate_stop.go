package cli

import (
	"io"

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
	out, errOut, code := tdd.RenderStopVerdict(event, tdd.DecideStop(event, raw))
	stdout.Write(out)
	stderr.Write(errOut)
	return code
}
