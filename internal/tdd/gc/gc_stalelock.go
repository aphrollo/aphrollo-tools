package gc

// A build lock is only worth what its holder is: the OS lock itself releases
// when the holding process dies, but a target dir whose lock cannot be taken
// for any OTHER reason would be kept forever by a sweep that only ever asks
// "is it free?". Issue #565 measured the cost: 7.5 GB idle for three days,
// refused run after run, on a lock whose recorded holder was not in the
// process list at all.

// staleTargetLock reports whether a target dir's build lock is protecting
// nothing: its recorded holder is a process that is provably gone. EVERY
// other answer keeps the protection — no owner record at all, an unreadable
// one, or a pid the OS will not answer for (pidRunning reports a process it
// cannot ask about as running) — because deleting a target dir a live build
// is writing into costs that build, while keeping a stale one costs a line in
// the next report.
func staleTargetLock(targetDir string) bool {
	owner, ok := ReadBuildSlotOwner(targetDir)
	if !ok || owner.PID <= 0 {
		return false
	}
	return !pidRunningFn(owner.PID)
}
