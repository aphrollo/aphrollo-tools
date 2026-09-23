package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A deferred job leaves three files: the record, the build's log and its
// result. They are keyed by project AND session, so every session that ever
// deferred a build in any repo left a set behind and nothing read them again.
// The ceiling on a running job is deferredMaxEnv (600s default), so a file a
// day old belongs to a job that finished, was abandoned, or died with the
// session that started it.
//
// This sweep and its PID kill never touch a mutation job (mutants_job.go):
// those are a separate mechanism with their own file set in a different
// state subdirectory, legitimately hours long by design with no ceiling
// here to misapply. A deferred build/run phase is the one this file reaps,
// and it is the one explicitly documented as short (600s).

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
// deferredJobMaxAge ago. Before it drops a job RECORD (not its log or result)
// it makes a best-effort attempt to kill whatever PID it still names: this is
// the backstop for a session that never fired EndSession at all (a crash, a
// killed terminal) — reapSessionDeferredJobs handles the graceful exit, but
// nothing calls it when there was no exit to hook. A record this old belongs
// to a phase long past deferredMax regardless, so killing it costs nothing a
// healthy build could lose. Best-effort in every direction: a file it cannot
// stat is left alone, an unreadable or undecodable record is skipped rather
// than killed, and a delete that fails is not an error anyone can act on --
// the next sweep tries again.
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
		name := e.Name()
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".result.json") {
			killLivePID(path)
		}
		if os.Remove(path) == nil {
			removed++
		}
	}
	return removed
}

// killLivePID ends whatever process a day-old deferred-job record still
// names, before the sweep deletes the record. A job record decodes to PID 0
// when nothing was ever recorded as spawned; that is left alone. A day is
// long enough for the OS to have handed the same PID to something this
// record never named — pidStillOurs is what tells the two apart before the
// kill goes out; the record is dropped either way.
func killLivePID(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	j, ok := decodeJob(data)
	if !ok || j.PID <= 0 {
		return
	}
	if !pidStillOurs(j) {
		return
	}
	killDeferredFn(j)
}

// deferredDirPath names the deferred dir WITHOUT creating it: a scan asks
// where the files are, and a scan that creates state directories is a scan
// that lies about what is there.
func deferredDirPath() string {
	base := StateDir()
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
