package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The box-wide mutation-run lock is a non-blocking flock, and a flock has no
// queue: when the holder releases it, whichever waiter happens to retry first
// takes it. With every waiter polling on its own clock that is a lottery, and
// a lottery starves — a merge that had waited 88 minutes lost to one started
// long after it. The fairness is therefore kept beside the lock, in a queue of
// ticket files every account can read: a waiter takes a ticket on arrival,
// only the earliest live ticket may try the lock at all, and the ticket is
// handed back the moment its waiter stops waiting, whichever way that ends.
// `aphrollo status` reads the same tickets to list the queue.

// MutantsRunWaiter is one ticket in the mutation-run queue.
type MutantsRunWaiter struct {
	PID      int       `json:"pid"`
	Cwd      string    `json:"cwd"`
	Cmd      string    `json:"cmd"`
	Arrived  time.Time `json:"arrived"`
	Readable bool      `json:"-"`
	ticket   string
}

// mutantsRunTicketStaleAfter is how long a ticket may go without its waiter
// refreshing it before it counts as dead whatever its pid says. A pid can be
// reused by an unrelated process, and a dead waiter's ticket that still read
// as live would hold the whole queue behind it.
const mutantsRunTicketStaleAfter = 5 * time.Minute

// mutantsRunTicketHeartbeat is how often a waiter refreshes its own ticket:
// well inside mutantsRunTicketStaleAfter, so a slow tick never reads as death.
const mutantsRunTicketHeartbeat = 30 * time.Second

// mutantsRunQueuePollInterval is how often a waiter re-reads the queue. A
// run it waits for takes minutes to hours, so a tenth of a second of hand-off
// latency is nothing, and reading a directory fifty times a second is not.
const mutantsRunQueuePollInterval = 100 * time.Millisecond

// mutantsRunQueueDir holds one ticket per waiting acquirer, in the shared
// lock dir and under the same isolation seam as every other lock artefact.
func mutantsRunQueueDir() string {
	return filepath.Join(lockDir(), "mutants-run-queue")
}

// mutantsRunTicketName is a ticket's file name. The arrival time leads it,
// zero-padded, so the names sort in arrival order; the pid follows it to break
// a tie and to let a reader judge liveness without opening the file.
func mutantsRunTicketName(pid int, arrived time.Time) string {
	return fmt.Sprintf("%020d-%d.json", arrived.UnixNano(), pid)
}

// writeMutantsRunTicket enqueues pid as having arrived at arrived and returns
// the ticket file's path.
func writeMutantsRunTicket(pid int, arrived time.Time, cmd, cwd string) string {
	dir := mutantsRunQueueDir()
	path := filepath.Join(dir, mutantsRunTicketName(pid, arrived))
	w := MutantsRunWaiter{PID: pid, Cwd: cwd, Cmd: cmd, Arrived: arrived.UTC()}
	if data, err := json.MarshalIndent(w, "", "  "); err == nil {
		if err := ensureSharedSubdir(dir); err == nil {
			_ = writeSharedRecord(path, data)
		}
	}
	return path
}

// liveMutantsRunQueue lists the queue's live tickets, earliest first. A ticket
// whose pid is not running, or which its waiter stopped refreshing, is left
// out: a waiter killed mid-wait never hands its ticket back. Removing such a
// ticket is best-effort, since the sticky lock dir lets only its owner's
// account delete it; skipping it is what keeps the queue moving.
func liveMutantsRunQueue(now time.Time) []MutantsRunWaiter {
	dir := mutantsRunQueueDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []MutantsRunWaiter
	for _, e := range entries {
		nanos, pid, ok := parseMutantsRunTicketName(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !pidRunningFn(pid) || now.Sub(info.ModTime()) > mutantsRunTicketStaleAfter {
			_ = os.Remove(path)
			continue
		}
		w := MutantsRunWaiter{PID: pid, Arrived: time.Unix(0, nanos)}
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &w) == nil {
			w.Readable = true
		}
		w.PID, w.ticket = pid, e.Name()
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ticket < out[j].ticket })
	return out
}

// parseMutantsRunTicketName splits "<arrival nanos>-<pid>.json".
func parseMutantsRunTicketName(name string) (nanos int64, pid int, ok bool) {
	stem, isJSON := strings.CutSuffix(name, ".json")
	at, p, found := strings.Cut(stem, "-")
	if !isJSON || !found {
		return 0, 0, false
	}
	nanos, err1 := strconv.ParseInt(at, 10, 64)
	pid, err2 := strconv.Atoi(p)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return nanos, pid, true
}

// queuePosition is ticket's 1-based place in queue, 0 when it is not there.
func queuePosition(queue []MutantsRunWaiter, ticket string) int {
	for i, w := range queue {
		if w.ticket == ticket {
			return i + 1
		}
	}
	return 0
}

// describeMutantsRunWaiter names one waiter for the status report.
func describeMutantsRunWaiter(w MutantsRunWaiter) string {
	if !w.Readable {
		return fmt.Sprintf("an unreadable waiter (pid %d)", w.PID)
	}
	return fmt.Sprintf("%q in %s (pid %d)", w.Cmd, w.Cwd, w.PID)
}
