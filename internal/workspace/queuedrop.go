package workspace

import (
	"strconv"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// queuedDropWindow bounds how long after the verb queued a PR the sync keeps
// reading its timeline: a PR nobody merged or recorded for longer is not going
// to be.
const queuedDropWindow = 14 * 24 * time.Hour

// recordDroppedQueued records the queue red of each PR the verb queued without
// waiting that the merge queue then dropped for failed checks, so the lane's
// first-run result does not depend on someone running `--wait`. A PR with a
// merge record of the verb's, or a queue red already, is left alone. Best
// effort: an unreadable timeline records nothing.
func recordDroppedQueued(repo string) {
	primary := core.PrimaryCheckoutRoot(repo)
	if primary == "" {
		return
	}
	type queuedPR struct {
		lane string
		at   time.Time
	}
	queued := map[int]queuedPR{}
	settled := map[int]bool{}
	for _, e := range core.ReadEvents(repo) {
		pr, err := strconv.Atoi(e.Detail["pr"])
		if err != nil || !sameDir(e.Repo, primary) {
			continue
		}
		switch {
		case e.Kind == "merge" && e.Verdict == "queued":
			if at, err := time.Parse(time.RFC3339, e.At); err == nil {
				queued[pr] = queuedPR{e.Lane, at}
			}
		// A PR the queue merged is read again: it may have been dropped for failed
		// checks and queued by hand after.
		case e.Kind == "merge" && e.Detail["method"] != "merge queue", e.Kind == "ci" && e.Detail["ci"] == ciQueue:
			settled[pr] = true
		}
	}
	for pr, q := range queued {
		if settled[pr] || time.Since(q.at) > queuedDropWindow {
			continue
		}
		rem, err := ghQueueRemoval(repo, "", pr)
		if err != nil || !rem.FailedChecks {
			continue
		}
		recordQueueRed(repo, q.lane, pr)
	}
}
