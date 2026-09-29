package tdd

import "os"

// GCAfterRun sweeps what a finished merge queue or mutation run left behind
// for repoRoot: the mutation areas and temp copies, the scratch of killed
// runs in the OS temp dirs, and the lock litter. It is the unattended half of
// housekeeping that a session-start sweep cannot be, because the litter of a
// run that just ended is exactly what a once-a-day sweep leaves for a day.
//
// Best effort and silent: it returns the bytes freed, and nothing it cannot
// prove dead is touched. Three things keep it off a live run's ground:
// ScanGC leaves every mutation area alone while a run holds the box-wide
// mutation lock, a directory some live process holds is never scratch, and a
// merge queue this process does not own that is still alive (its record names
// a pid and the identity of the process behind it, so a recycled pid is not
// mistaken for it) suspends the whole pass.
func GCAfterRun(repoRoot string) int64 {
	if repoRoot == "" {
		return 0
	}
	if rec, err := LoadMergeQueueRecord(repoRoot); err == nil && rec != nil && rec.PID != os.Getpid() && rec.Live() {
		return 0
	}
	age, err := ParseGCAge("3d")
	if err != nil {
		return 0
	}
	scope := GCScope{Mutants: true, TempLitter: true, TempScratch: true}
	freed, _, _ := ApplyGCFor(repoRoot, ScanGC(repoRoot, age, scope))
	return freed
}
