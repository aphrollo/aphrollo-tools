//go:build !windows

package tdd

// machineLoadSample has no implementation outside Windows yet — #526 was
// filed against a Windows box (tasklist/MSYS quirks, GetProcessTimes), and
// every other reader of this seam already treats ok=false as "load
// unavailable" rather than guessing, so a non-Windows gate degrades the same
// way an unreadable Windows sample would.
func machineLoadSample() (cores int, loadPct float64, procs []procSample, ok bool) {
	return 0, 0, nil, false
}
