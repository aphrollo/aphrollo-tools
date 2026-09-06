package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file backs `aphrollo gate status` (issue #430): a session staring at
// a BUILDING/TIMEOUT/QUEUED-SKIPPED advisory line had nowhere to look but a
// rerun, which queues behind the very work it is trying to observe. The
// three functions here answer, read-only, what deferred_edit.go's own
// bookkeeping already knows: which edit jobs are running, and — through
// WaitDeferredEditJob — how to wait on one instead of polling by hand.

// allDeferredJobRecords reads every job record in the deferred dir,
// regardless of whether it has finished. Best-effort: an unreadable
// directory or one torn record is skipped rather than failing the whole
// list, the posture readStateJSON already takes for a single file.
func allDeferredJobRecords() []DeferredJob {
	dir := deferredDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var jobs []DeferredJob
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".result.json") {
			continue
		}
		var j DeferredJob
		if ok, _ := readStateJSON(filepath.Join(dir, name), &j); !ok {
			continue
		}
		jobs = append(jobs, j)
	}
	return jobs
}

// ActiveDeferredJobs lists every deferred edit job on this box that has not
// yet produced a result AND whose process can be confirmed still running —
// one entry per project+session pair genuinely still going. Exported for
// `gate status`. A job with no result whose pid is dead, expired past the
// deferral ceiling, or unverifiable is not "abandoned but not yet swept" here
// — it is excluded outright, so the report never reads a day-old dead job as
// running (issue #451): the 24h file sweep is what eventually deletes the
// record, this is only about what the report SAYS while it is waiting to.
func ActiveDeferredJobs() []DeferredJob {
	now := time.Now()
	var active []DeferredJob
	for _, j := range allDeferredJobRecords() {
		if _, done := deferredResult(j); done {
			continue
		}
		if !deferredJobLive(j, now) {
			continue
		}
		active = append(active, j)
	}
	return active
}

// deferredJobLive reports whether a deferred job with no result yet is one a
// caller should actually wait on. Unlike pidStillOurs — a KILL site, where
// "cannot tell" defaults to permission to act, because the cost of a wrong
// "yes" is a live build losing its warm cache — nothing here deletes or
// kills anything, so this defaults the other way: an unresolvable pid must
// never be reported as a definite "running" (issue #451). A dead pid, a
// ceiling-expired job, and a record with no PIDCreatedAt to check identity
// against (so a recycled pid on Windows could be a different process
// entirely) are all treated the same: not live.
func deferredJobLive(j DeferredJob, now time.Time) bool {
	if j.PID <= 0 || deferredExpired(j, now) {
		return false
	}
	live, ok := processStartTimeFn(j.PID)
	if !ok {
		return false
	}
	if j.PIDCreatedAt.IsZero() {
		return false
	}
	return live.Sub(j.PIDCreatedAt).Abs() <= pidIdentityTolerance
}

// findDeferredJobForProject returns the most recently started deferred job
// recorded for root, across every session — `gate status` has no session id
// of its own to key on the way a hook's job record does, so it matches on
// the project path instead.
func findDeferredJobForProject(root string) (DeferredJob, bool) {
	var best DeferredJob
	found := false
	for _, j := range allDeferredJobRecords() {
		if !sameDeferredProject(j.Project, root) {
			continue
		}
		if !found || j.Started.After(best.Started) {
			best, found = j, true
		}
	}
	return best, found
}

// waitDeferredPollInterval is how often WaitDeferredEditJob re-checks a
// running job. Short enough that a session waiting on it does not notice the
// polling; long enough not to hammer the disk over a build that legitimately
// takes minutes.
const waitDeferredPollInterval = 2 * time.Second

// WaitDeferredEditJob blocks until root's own deferred edit job, if any,
// reaches a verdict, then returns EXACTLY the advisory line a hook harvesting
// it would have printed — it calls the same harvestDeferred a hook does, in
// a loop, rather than re-deriving the verdict a second way. ok=false means
// there is nothing recorded for root to wait for (already harvested, or
// never deferred).
//
// The loop keys on the BUILDING prefix, not on harvestDeferred's own
// startFresh return: startFresh answers "should a HOOK go on to run its own
// edit's phases", which is false both while a phase is still running (a
// non-empty "still BUILDING" line) and once it has finished with a real
// verdict (an equally non-empty, non-BUILDING line) — the two cases this
// loop exists to tell apart.
func WaitDeferredEditJob(root string) (advisory string, ok bool) {
	j, found := findDeferredJobForProject(root)
	if !found {
		return "", false
	}
	if _, done := deferredResult(j); !done && !deferredJobLive(j, time.Now()) {
		// No result yet, and the process behind this record cannot be
		// confirmed alive: a zombie record, not a run to wait on. Reported
		// as "nothing recorded" rather than polled to the deferral
		// ceiling — the whole point of issue #451 is that a caller must
		// never be left blocking on a pid that is already gone.
		return "", false
	}
	headSHA := headSHAFor(root)
	for {
		line, _ := harvestDeferred(root, headSHA, j.FileHash, j.Session, waitDeferredPollInterval, nil, "")
		if strings.HasPrefix(line, "gate: → BUILDING") {
			time.Sleep(waitDeferredPollInterval)
			continue
		}
		return line, line != ""
	}
}
