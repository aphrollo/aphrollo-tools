package tdd

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A deferred job leaves three files: the record, the build's log and its
// result. They are keyed by project AND session, so every session that ever
// deferred a build in any repo left a set behind and nothing read them again.
// The ceiling on a running job is deferredMaxEnv (600s default), so a file a
// day old belongs to a job that finished, was abandoned, or died with the
// session that started it.

// deferredJobMaxAge is when a deferred job's files stop being evidence. Well
// past any real build, and past the deferral ceiling by two orders of
// magnitude: nothing this old is still running.
const deferredJobMaxAge = 24 * time.Hour

// deferredSweepOnce keeps the sweep to one directory read per PROCESS. A hook
// is a process, so every hook still sweeps; a long-lived one does not re-read
// the directory on every statusline render.
var deferredSweepOnce sync.Once

// sweepDeferredJobsOnce runs the sweep the first time this process reads a
// job record, which is the moment the directory is being consulted anyway.
func sweepDeferredJobsOnce() {
	deferredSweepOnce.Do(func() { sweepDeferredJobs(time.Now()) })
}

// resetDeferredSweepForTest lets a test observe the once-per-process sweep
// more than once in one binary.
func resetDeferredSweepForTest() { deferredSweepOnce = sync.Once{} }

// sweepDeferredJobs removes every deferred-job file last touched more than
// deferredJobMaxAge ago. Best-effort in both directions: a file it cannot
// stat is left alone, and a delete that fails is not an error anyone can act
// on -- the next sweep tries again.
func sweepDeferredJobs(now time.Time) int {
	dir := deferredDirPath()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= deferredJobMaxAge {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed
}

// deferredDirPath names the deferred dir WITHOUT creating it: a scan asks
// where the files are, and a scan that creates state directories is a scan
// that lies about what is there.
func deferredDirPath() string {
	base := stateDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "deferred")
}

// gcDeferredJobFiles proposes the same files the automatic sweep takes, so an
// operator reading `gate gc` sees where they went instead of finding a
// directory that quietly empties itself.
func gcDeferredJobFiles(dir string, olderThan time.Duration, now time.Time) []GCCandidate {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= olderThan {
			continue
		}
		out = append(out, GCCandidate{
			Path:   filepath.Join(dir, e.Name()),
			Size:   info.Size(),
			Reason: "deferred build record, idle " + formatDays(now.Sub(info.ModTime())),
			Kind:   GCKindTempLitter,
		})
	}
	return out
}
