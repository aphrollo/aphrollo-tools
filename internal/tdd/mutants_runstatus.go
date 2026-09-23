package tdd

import (
	"fmt"
	"strings"
	"time"
)

// MutantsRunStatus is the box-wide mutation-run lock as `aphrollo status`
// reports it. Held with OwnerKnown false is its own state, never folded into
// idle: the lock is taken and the record naming the holder cannot be read,
// which is what a session saw for forty minutes while a CI run held the lock
// under a record only the runner's account could open.
type MutantsRunStatus struct {
	Held       bool
	OwnerKnown bool
	Owner      BuildLockOwner
	// Queue is every live waiter, earliest first.
	Queue []MutantsRunWaiter
}

// SnapshotMutantsRun reads the holder record first and trusts it once its pid
// is confirmed alive. Without a trustworthy record the lock itself is the
// evidence: one non-blocking attempt, released at once, answers whether
// anything holds it. A waiter at the head of the queue polls the same lock,
// so the probe can cost it at most one poll interval.
func SnapshotMutantsRun() MutantsRunStatus {
	s := snapshotMutantsRunHolder()
	s.Queue = liveMutantsRunQueue(time.Now())
	return s
}

// snapshotMutantsRunHolder is the holder half of SnapshotMutantsRun.
func snapshotMutantsRunHolder() MutantsRunStatus {
	if o, ok := readBuildLockOwnerAt(mutantsRunLockOwnerPath()); ok && pidRunningFn(o.PID) {
		return MutantsRunStatus{Held: true, OwnerKnown: true, Owner: o}
	}
	if release, acquired := TryAcquireFileLock(mutantsRunLockPath()); acquired {
		release()
		return MutantsRunStatus{}
	}
	return MutantsRunStatus{Held: true}
}

// FormatMutantsRunStatus renders the mutation-run section of the status
// report: one holder line, whichever of the three states holds (idle, held by
// a named owner, held by an owner whose record is unreadable), then the queue
// in the order it will be served.
func FormatMutantsRunStatus(s MutantsRunStatus, now time.Time) string {
	var b strings.Builder
	b.WriteString("mutation run:\n")
	switch {
	case !s.Held:
		b.WriteString("  idle\n")
	case s.OwnerKnown:
		fmt.Fprintf(&b, "  held by %s, running %s\n", describeMutantsRunHolder(s), formatElapsedSecs(now.Sub(s.Owner.Started)))
	default:
		fmt.Fprintf(&b, "  held by %s\n", describeMutantsRunHolder(s))
	}
	for i, w := range s.Queue {
		fmt.Fprintf(&b, "  queued %d of %d: %s, waiting %s\n", i+1, len(s.Queue), describeMutantsRunWaiter(w), formatElapsedSecs(now.Sub(w.Arrived)))
	}
	return b.String()
}

// describeMutantsRunHolder names a held lock's holder, or says in so many
// words that the record naming it cannot be read.
func describeMutantsRunHolder(s MutantsRunStatus) string {
	if s.OwnerKnown {
		return describeOwner(s.Owner) + staleHolderNotice(s.Owner.PID)
	}
	return "an unreadable owner (no readable record at " + mutantsRunLockOwnerPath() + ")"
}
