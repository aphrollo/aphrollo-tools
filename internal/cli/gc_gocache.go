package cli

import "github.com/aphrollo/aphrollo-tools/internal/tdd"

// sweepGoCache is the Go build cache's part of a gate gc run: the line the run
// prints for it, "" when there is nothing to say. The detached session-start
// sweep (background) trims at most once per six hours; a run an operator
// types always does. A dry run reports the plan and neither trims nor stamps.
func sweepGoCache(s tdd.GoCacheSettings, apply, background, quiet bool) string {
	if quiet && !apply {
		return "" // a quiet dry run prints nothing, so it has no use for the walk
	}
	if background && apply {
		if !tdd.GoCacheTrimDue() {
			return ""
		}
		tdd.StampGoCacheTrim()
	}
	return tdd.RenderGoCacheTrim(tdd.TrimGoCache(s, apply), apply)
}
