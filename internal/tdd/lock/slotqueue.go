package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// A deferred edit phase queues for its build slot for minutes. Before this,
// each edit's request queued on its own, so four edits of one file stacked
// four identical builds behind one target dir (issue #830), every one of
// them compiling a tree state the next edit had already replaced.
//
// So a waiting request leaves a record under slot-queue/, keyed by target
// dir, working directory and command. A newer identical request overwrites
// the record, and the older one, polling, finds a live request other than
// itself there and gives up as SlotSuperseded: the newer request builds the
// same command over the newer tree state. A request that already holds its
// slot never looks again, so only a queued, not-yet-started build is ever
// replaced. A different command in the same target dir is different work
// and keys a different record.

// SlotWait is how a queued slot request ended.
type SlotWait int

const (
	// SlotHeld means the request holds its target lock and a global slot.
	SlotHeld SlotWait = iota
	// SlotTimedOut means the deadline passed with the target still busy.
	SlotTimedOut
	// SlotSuperseded means a newer identical request replaced this one
	// while it waited; that request builds the command instead.
	SlotSuperseded
)

// slotRequest is one waiting request's record: its process and the token
// that tells two requests from the same process apart.
type slotRequest struct {
	PID   int    `json:"pid"`
	Token string `json:"token"`
}

// slotRequestSeq makes tokens unique within one process.
var slotRequestSeq atomic.Int64

// slotRequestQueuedHook is called with a request's record path once the
// record is written. A seam for tests, which need to know a request is
// queued before they start the next one.
var slotRequestQueuedHook = func(string) {}

// SetSlotRequestQueuedHookForTest installs fn as the queued-request seam and
// returns the restore, for a test in a package above this one.
func SetSlotRequestQueuedHookForTest(fn func(path string)) (restore func()) {
	prev := slotRequestQueuedHook
	slotRequestQueuedHook = fn
	return func() { slotRequestQueuedHook = prev }
}

// slotRequestPath names the one record identical requests share.
func slotRequestPath(target, cmd, cwd string) string {
	sum := sha256.Sum256([]byte(cwd + "\x00" + cmd))
	name := targetDirKey(target) + "." + hex.EncodeToString(sum[:8]) + ".json"
	return filepath.Join(lockDir(), "slot-queue", name)
}

// readSlotRequest reads a request record. false for a missing or torn one,
// which the caller treats as "nobody newer".
func readSlotRequest(path string) (slotRequest, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return slotRequest{}, false // absence-ok: no record means no newer request queued
	}
	var r slotRequest
	if json.Unmarshal(data, &r) != nil || r.Token == "" {
		return slotRequest{}, false
	}
	return r, true
}

// enqueueSlotRequest writes this request's record over any older identical
// request's, and returns the record's path and this request's token.
// Best-effort like every lock artefact: an unwritable record costs the
// coalescing, never the wait.
func enqueueSlotRequest(target, cmd, cwd string) (path, token string) {
	path = slotRequestPath(target, cmd, cwd)
	token = fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), slotRequestSeq.Add(1))
	data, err := json.Marshal(slotRequest{PID: os.Getpid(), Token: token})
	if err == nil && ensureSharedSubdir(filepath.Dir(path)) == nil && writeSharedRecord(path, data) == nil {
		slotRequestQueuedHook(path)
	}
	return path, token
}

// slotRequestSuperseded reports whether a live request other than token's
// now owns the record. A dead one does not count: its build will never run.
func slotRequestSuperseded(path, token string) bool {
	r, ok := readSlotRequest(path)
	return ok && r.Token != token && pidRunningFn(r.PID)
}

// leaveSlotQueue removes the record once a request ends. The record may
// already be a newer request's; removing it costs that request nothing,
// because a missing record never supersedes anyone.
func leaveSlotQueue(path string) {
	_ = os.Remove(path)
}

// acquireQueuedBuildSlot is acquireBuildSlot for a request that may be
// replaced while it waits: it polls for both locks until they are held, the
// deadline passes, or a newer identical request supersedes it.
func acquireQueuedBuildSlot(targetDir string, deadline time.Duration, cmd, cwd string) (BuildSlot, func(), SlotWait) {
	path, token := enqueueSlotRequest(targetDir, cmd, cwd)
	defer leaveSlotQueue(path)
	return waitForBuildSlot(targetDir, deadline, cmd, cwd, func() bool { return slotRequestSuperseded(path, token) })
}
