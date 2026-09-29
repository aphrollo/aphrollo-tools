package cli

import "github.com/aphrollo/aphrollo-tools/internal/tdd"

// gcAfterRunFn is the sweep that follows a merge queue or a mutation run,
// behind a variable so a test observes it without touching the box's real
// temp dirs.
var gcAfterRunFn = tdd.GCAfterRun

// sweepAfterRun reclaims what a run that just ended left in repo's areas and
// the OS temp dirs. Best effort and silent: housekeeping never changes a
// verb's outcome.
func sweepAfterRun(repo string) {
	if repo == "" {
		return
	}
	gcAfterRunFn(repo)
}
