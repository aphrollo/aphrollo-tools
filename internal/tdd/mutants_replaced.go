package tdd

import (
	"fmt"
	"path/filepath"
	"strings"
)

// self-install (and `aphrollo update`) rename the running binary aside and
// let whatever already holds it keep executing — the exact scenario #311
// gave a QUEUE-side notice for: a waiter behind the box-wide mutation-run
// lock is told the holder's binary was replaced since it started. The
// deploy-path half that issue left undone is this file: naming those jobs
// at INSTALL time, when the installer already knows it just replaced the
// binary and RunningMutantsJobs already holds every pid, rather than only
// to whoever happens to queue behind one later (#338).

// JobsRunningReplacedBinary is every live mutation job, across EVERY repo's
// registry, whose process is still executing the exact file self-install
// just renamed aside to stalePath. Those jobs hold the box-wide
// mutation-run lock and produce results from code that is no longer
// installed. "" (nothing existed at bin yet, so nothing was renamed) reports
// nothing.
func JobsRunningReplacedBinary(stalePath string) []MutantsJob {
	if stalePath == "" {
		return nil
	}
	var out []MutantsJob
	for _, j := range allRunningMutantsJobs() {
		if p, ok := processExePathFn(j.PID); ok && p == stalePath {
			out = append(out, j)
		}
	}
	return out
}

// allRunningMutantsJobs is RunningMutantsJobs generalized across every
// repo's registry file in mutantsStateDir: self-install replaces the ONE
// machine-wide binary, so a job it must warn about can belong to any repo on
// the box, not just the one --repo names.
func allRunningMutantsJobs() []MutantsJob {
	dir := mutantsStateDir()
	if dir == "" {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "jobs.*.json"))
	if err != nil {
		return nil
	}
	var out []MutantsJob
	for _, path := range matches {
		out = append(out, liveJobsAt(path)...)
	}
	return out
}

// ReplacedBinaryJobsLine renders JobsRunningReplacedBinary's finding as the
// one line self-install prints, "" when nothing is still running the
// replaced binary — the common case, which must stay silent.
func ReplacedBinaryJobsLine(stalePath string) string {
	jobs := JobsRunningReplacedBinary(stalePath)
	if len(jobs) == 0 {
		return ""
	}
	names := make([]string, 0, len(jobs))
	for _, j := range jobs {
		names = append(names, fmt.Sprintf("%s pid %d", jobLabel(j), j.PID))
	}
	return fmt.Sprintf("gate: %d mutation run(s) are still executing the binary just replaced (%s) — their results predate this install",
		len(jobs), strings.Join(names, ", "))
}

// jobLabel names a job the way an operator would look for it: its lane
// branch when it has one, the bare repo otherwise (a run on main/master, or
// a record from before branches were tracked).
func jobLabel(j MutantsJob) string {
	if j.Branch != "" {
		return j.Branch
	}
	return j.Repo
}
