package tdd

import (
	"fmt"
	"strings"
	"time"
)

// FormatGateStatus renders `aphrollo gate status`'s one report: every
// deferred edit job on the box, every global build slot's holder, and this
// checkout's own mutation-run state — the three sources an inconclusive
// gate line (issue #430) now tells a session to go look at instead of
// rerunning into the same queue.
func FormatGateStatus(jobs []DeferredJob, slots []BuildSlotStatus, mutants MutantsStatusReport, mutantsErr error, now time.Time) string {
	var b strings.Builder
	b.WriteString("deferred edit jobs:\n")
	if len(jobs) == 0 {
		b.WriteString("  none running\n")
	} else {
		for _, j := range jobs {
			fmt.Fprintf(&b, "  %s [%s] pid %d, running %s\n", j.Project, j.Phase, j.PID, formatElapsedSecs(now.Sub(j.Started)))
		}
	}
	fmt.Fprintf(&b, "build slots (%d):\n", len(slots))
	for _, s := range slots {
		if !s.Held {
			fmt.Fprintf(&b, "  slot %d: idle\n", s.Index)
			continue
		}
		fmt.Fprintf(&b, "  slot %d: held by %s, running %s\n", s.Index, describeOwner(s.Owner), formatElapsedSecs(now.Sub(s.Owner.Started)))
	}
	b.WriteString("mutation run (this checkout):\n")
	if mutantsErr != nil {
		fmt.Fprintf(&b, "  %v\n", mutantsErr)
	} else {
		line, _ := FormatMutantsStatus(mutants)
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

// formatElapsedSecs renders a duration the way the rest of gate status's
// report does — whole seconds, never negative (a clock read racing a
// just-started job must not print "-1s").
func formatElapsedSecs(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%ds", int(d.Seconds()+0.5))
}
