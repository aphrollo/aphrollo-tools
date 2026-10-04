package postedit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// A session has one detached run per project at a time (the job record is keyed
// by session and project). An edit made while that run is going used to get the
// running job's BUILDING line and nothing else: its own tests were not started,
// not queued, and not named (issue #1188), so the compile error it introduced
// went unreported. Now the edit's run waits in a per-project queue and starts
// when the slot frees; the edit's own line says which run that is and that it
// has not run yet.
//
// The queue is first in, first out, one entry per run: a newer edit of the same
// command and directory replaces the one waiting, because the run reads the
// tree as it stands when it starts and the older request would only repeat it.
// It is bounded: past maxQueuedRuns an edit is told plainly that it was not
// tested, the one place work is dropped.

// maxQueuedRuns bounds one project's waiting runs.
const maxQueuedRuns = 6

// queuedRun is one edit's run, waiting for the slot.
type queuedRun struct {
	Runner []string `json:"runner"`
	Dir    string   `json:"dir"`
	File   string   `json:"file"`
	EditID string   `json:"edit_id,omitempty"`
	// Touched is every other file the same write changed under the project.
	Touched []string  `json:"touched,omitempty"`
	At      time.Time `json:"at"`
}

// runQueue is the waiting runs of one session in one project.
type runQueue struct {
	Schema  int         `json:"schema"`
	Project string      `json:"project"`
	Runs    []queuedRun `json:"runs"`
}

// queueFileSuffix keeps a queue apart from the job records, which end in
// ".json" and are listed by that.
const queueFileSuffix = ".queue"

func queuePath(session, root string) string {
	dir := deferredDir()
	if dir == "" {
		return ""
	}
	name := projectKey(root)
	if session != "" {
		name += "-" + sessionKey(session)
	}
	return filepath.Join(dir, name+queueFileSuffix)
}

// sameUnit is whether a waiting request is the run argv in dir: the same
// command in the same directory.
func (q queuedRun) sameUnit(argv []string, dir string) bool {
	return q.Dir == dir && slices.Equal(q.Runner, argv)
}

// queueOutcome is what became of an edit's request.
type queueOutcome struct {
	// position is the 1-based place of the request among the waiting runs.
	position int
	// waiting is how many runs wait, this one included.
	waiting int
	// replaced says an older request for the same run was waiting and this one
	// took its place.
	replaced bool
	// full says the queue was at its bound and the request was not kept.
	full bool
}

// enqueueRun puts an edit's run in the project's queue, under the file's lock.
func enqueueRun(session, root string, req queuedRun) queueOutcome {
	path := queuePath(session, root)
	if path == "" {
		return queueOutcome{full: true}
	}
	release := acquirePathLock(path)
	defer release()
	q := readQueue(path)
	q.Project = root
	out := queueOutcome{}
	if i := slices.IndexFunc(q.Runs, func(r queuedRun) bool { return r.sameUnit(req.Runner, req.Dir) }); i >= 0 {
		q.Runs = slices.Delete(q.Runs, i, i+1)
		out.replaced = true
	}
	if len(q.Runs) >= maxQueuedRuns {
		writeQueue(path, q)
		return queueOutcome{full: true, waiting: len(q.Runs)}
	}
	q.Runs = append(q.Runs, req)
	out.position, out.waiting = len(q.Runs), len(q.Runs)
	writeQueue(path, q)
	return out
}

// dropQueuedRun forgets a waiting request for a run that is starting now: the
// run reads the tree as it stands, so the waiting one has nothing to add.
func dropQueuedRun(session, root string, argv []string, dir string) {
	path := queuePath(session, root)
	if path == "" {
		return
	}
	release := acquirePathLock(path)
	defer release()
	q := readQueue(path)
	n := len(q.Runs)
	q.Runs = slices.DeleteFunc(q.Runs, func(r queuedRun) bool { return r.sameUnit(argv, dir) })
	if len(q.Runs) != n {
		writeQueue(path, q)
	}
}

func readQueue(path string) runQueue {
	var q runQueue
	if ok, _ := readStateJSON(path, &q); !ok {
		return runQueue{}
	}
	return q
}

// writeQueue saves q, or removes the file when nothing waits.
func writeQueue(path string, q runQueue) {
	if len(q.Runs) == 0 {
		_ = os.Remove(path)
		return
	}
	q.Schema = StateSchema
	if data, err := json.MarshalIndent(q, "", "  "); err == nil {
		_ = writeFileAtomic(path, data)
	}
}

// pumpQueue starts the oldest waiting run of a project when the slot is free:
// no job is recorded for the session there, which means the last one was
// harvested and reported. The run starts from its first phase, on the tree as
// it stands, and its verdict reaches a later hook like any deferred run's.
//
// The queue's lock is held from the check that the slot is free to the spawn
// that takes it, so two hooks of one session cannot both start a run into the
// one job record. The lines it returns are the hook's to print: a run that
// started, a run that could not (the edit was told it would start), and an
// entry that waited so long behind a job that never finished that it was given
// up on — each a fact about an edit that was promised a run.
func pumpQueue(session, root string) []string {
	path := queuePath(session, root)
	if path == "" {
		return nil
	}
	release := acquirePathLock(path)
	defer release()
	q := readQueue(path)
	if len(q.Runs) == 0 {
		return nil
	}
	var lines []string
	kept := q.Runs[:0:0]
	for _, r := range q.Runs {
		if time.Since(r.At) > deferredMax() {
			lines = append(lines, queueDroppedLine(r, root, "it waited longer than a run may live behind a job that never finished"))
			continue
		}
		kept = append(kept, r)
	}
	q.Runs = kept
	if _, busy := loadDeferredJob(session, root); busy || len(q.Runs) == 0 {
		writeQueue(path, q)
		return lines
	}
	req := q.Runs[0]
	q.Runs = q.Runs[1:]
	writeQueue(path, q)
	runner := runnerFromArgv(req.Runner, req.Dir)
	first := firstEditPhase(runner, root, req.File, headSHAFor(root), sourceIdentity(root, req.File), session, req.EditID, req.Touched...)
	if _, ok := spawnPhaseFn(first); !ok {
		clearDeferredJob(first.Session, first.Project)
		AppendGateLog("postedit", root, cmdString(runner), InfraFailed, 0)
		return append(lines, spawnFailedQueuedLine(runner, root))
	}
	AppendGateLog("postedit", root, cmdString(runner), "queue-started", 0)
	return append(lines, queueStartedLine(runner, root))
}

// pumpSessionQueues pumps every project the session has runs waiting in and
// answers the lines the pumps printed.
func pumpSessionQueues(session string) []string {
	session = strings.TrimSpace(session)
	dir := deferredDirPath()
	if session == "" || dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var lines []string
	suffix := "-" + sessionKey(session) + queueFileSuffix
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		if q := readQueue(filepath.Join(dir, e.Name())); q.Project != "" {
			lines = append(lines, pumpQueue(session, q.Project)...)
		}
	}
	return lines
}

// queuedEscape is what a QUEUED line offers in place of the wait the BUILDING
// line offers: the run it names has not started, or runs on a tree that has
// already moved, so waiting on it in the foreground buys nothing (issue #1189).
const queuedEscape = "not tested yet — the verdict arrives at a later hook, and commit and precommit will judge it"

// queuedSameRunLine is the line of an edit whose run is the one already going,
// for a tree that has moved since it started: it restarts on the newest source
// when it ends, so the verdict now coming describes the older state.
func queuedSameRunLine(r Runner, root string) string {
	return fmt.Sprintf("gate: %s in %s → QUEUED (deferred; the same run is going for an older state of this tree and restarts on the newest source when it ends; %s)",
		cmdString(r), root, queuedEscape)
}

// queuedLine is the line of an edit whose run waits behind another run of the
// session.
func queuedLine(r Runner, root string, active []string, out queueOutcome) string {
	replaced := ""
	if out.replaced {
		replaced = ", replacing an older request for it"
	}
	return fmt.Sprintf("gate: %s in %s → QUEUED (deferred; %q is still going; this run starts when it ends, place %d of %d%s; %s)",
		cmdString(r), root, typedCommand(active), out.position, out.waiting, replaced, queuedEscape)
}

// typedCommand is a recorded command line as the edit would have typed it: the
// timeout the deferred run adds for the ceiling's sake is no part of the name.
func typedCommand(argv []string) string {
	var kept []string
	for _, a := range argv {
		if !strings.HasPrefix(a, "-timeout=") {
			kept = append(kept, a)
		}
	}
	return strings.Join(kept, " ")
}

// queueStartedLine is the line of a queued run that has just started: the edit
// was told it would, and this is the hook that says it did.
func queueStartedLine(r Runner, root string) string {
	return fmt.Sprintf("gate: %s in %s → BUILDING (deferred; the queued run started — result at a later hook; %s)", cmdString(r), root, buildingEscapeFor(root))
}

// spawnFailedQueuedLine is the line of a queued run that could not start.
func spawnFailedQueuedLine(r Runner, root string) string {
	return fmt.Sprintf("gate: %s in %s → %s (the queued run could not be started — the code was NOT tested)", cmdString(r), root, InfraFailed)
}

// queueDroppedLine is the line, and the gate-log entry, of a waiting run that
// will never start: nothing runs it, and the edit that asked for it was told it
// would. It is a not-tested outcome of that edit, counted as one.
func queueDroppedLine(r queuedRun, root, why string) string {
	cmd := typedCommand(r.Runner)
	AppendGateLog("postedit", root, cmd, "queued-dropped", 0)
	return fmt.Sprintf("gate: %s in %s → QUEUED-DROPPED (%s — the code was NOT tested; commit and precommit will judge it)", cmd, root, why)
}

// queueFullLine is the line of an edit that could not be kept: the code was not
// tested, and it says so.
func queueFullLine(r Runner, root string) string {
	return fmt.Sprintf("gate: %s in %s → QUEUED-SKIPPED (deferred; %d runs already wait behind the one going — inconclusive, the code was NOT tested; commit and precommit will judge it)",
		cmdString(r), root, maxQueuedRuns)
}

// dropSessionQueues forgets every run the session had waiting: its session is
// over, and nothing would harvest what they started. Each is an edit that was
// told its run would start and never got one, so each leaves a not-tested entry
// in the gate log (queued-dropped) for the stats and the digest to count.
func dropSessionQueues(session string) {
	session = strings.TrimSpace(session)
	dir := deferredDirPath()
	if session == "" || dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	suffix := "-" + sessionKey(session) + queueFileSuffix
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		release := acquirePathLock(path)
		q := readQueue(path)
		for _, r := range q.Runs {
			queueDroppedLine(r, q.Project, "its session ended before the run could start")
		}
		_ = os.Remove(path) // a file that stays is swept with the day-old ones
		release()
	}
}
