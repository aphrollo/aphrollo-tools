package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file answers issue #435's stated residual: a caller cannot ask the
// queue shim "am I queued, and behind what". `queued-skipped` was
// observable only as the last token in gate.log -- nothing recorded a
// WAITING cargo-shim invocation anywhere a separate process could read.
//
// QueueWaiter is that record: written by cargo_shim.go's own wait loop for
// the DURATION of its wait (never longer -- acquiring or giving up removes
// it), read from a wholly separate `aphrollo status` invocation via
// SnapshotQueueWaiters. The two processes can disagree only about
// liveness, which the snapshot resolves the same way SnapshotBuildSlots
// already does for build-slot owners (issue #435's other half): a waiter
// whose pid is confirmably dead is dropped, never reported as still
// blocking.
type QueueWaiter struct {
	PID     int       `json:"pid"`
	Cwd     string    `json:"cwd"`
	Cmd     string    `json:"cmd"`
	Target  string    `json:"target"`
	Started time.Time `json:"started"`
}

// queueWaitersDir holds one record per currently-waiting shim invocation,
// inside the same lock dir every other lock artefact lives in (and the same
// isolation seam a test overrides).
func queueWaitersDir() string {
	return filepath.Join(lockDir(), "queue-waiters")
}

// queueWaiterPath names one waiter's record: the target it is queued for
// plus its own pid, since more than one invocation can be queued for the
// same target dir at once.
func queueWaiterPath(target string, pid int) string {
	return filepath.Join(queueWaitersDir(), fmt.Sprintf("%s.%d.json", targetDirKey(target), pid))
}

// WriteQueueWaiter records THIS process as currently queued for target.
// Exported so internal/cli's cargo shim can write it around its own wait
// loop. The returned remove MUST be called once the wait ends, whichever
// way it ends (acquired or gave up) -- best-effort, like every other lock
// artefact here: a write failure costs a reader its ability to see this
// wait, never the wait itself.
func WriteQueueWaiter(target, cmd, cwd string) (remove func()) {
	path := queueWaiterPath(target, os.Getpid())
	w := QueueWaiter{PID: os.Getpid(), Cwd: cwd, Cmd: cmd, Target: target, Started: time.Now().UTC()}
	if data, err := json.MarshalIndent(w, "", "  "); err == nil {
		if err := os.MkdirAll(queueWaitersDir(), 0o777); err == nil {
			_ = os.WriteFile(path, data, 0o600)
		}
	}
	return func() { _ = os.Remove(path) }
}

// SnapshotQueueWaiters reads every queue-waiter record on the box,
// best-effort (an unreadable directory or a torn record is skipped, the
// same posture every other snapshot here takes), and drops any whose pid
// is not confirmably alive. A hard-killed shim never runs its own cleanup,
// so the record on disk otherwise survives its process -- reporting that
// as "still queued" is exactly the liveness gap issue #435 names, the same
// one #451 and this issue's other half already closed for deferred jobs
// and build slots.
func SnapshotQueueWaiters() []QueueWaiter {
	dir := queueWaitersDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []QueueWaiter
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var w QueueWaiter
		if err := json.Unmarshal(data, &w); err != nil {
			continue
		}
		if !pidRunningFn(w.PID) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// QueueWaitersForRoot is SnapshotQueueWaiters scoped to one checkout: `gate
// status` answers for the tree it is run in, never for another lane's
// invocation that happens to be queued on the same box, and a waiter's cwd
// (recorded at the moment it started waiting) is the same field
// SnapshotBuildSlots' own owner records key on.
func QueueWaitersForRoot(root string) []QueueWaiter {
	var out []QueueWaiter
	for _, w := range SnapshotQueueWaiters() {
		if sameProject(w.Cwd, root) {
			out = append(out, w)
		}
	}
	return out
}
