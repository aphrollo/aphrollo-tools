package gc

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Category (n): the scratch a killed run leaves in the OS temp dirs. A
// runaway suite is killed by the memory cap, a timeout or the kernel, and
// none of those runs its own cleanup: the go tool's `go-build<digits>` work
// dirs, the t.TempDir() of a test that never finished, and the stub and
// fixture dirs the gate's own test binaries build all stay behind. On the box
// issue #1005 was filed against, 296 go-build dirs older than two hours sat in
// a RAM-backed /tmp beside 998 pkgtest stub dirs and 1,472 gh stub dirs, and
// nothing swept them: the categories above look only inside a repo's own
// areas, and category (d) waits a day and matches only two name shapes.
//
// Who owns such a directory is not written in it, so ownership is read from
// the OS where it can be: a directory some live process has as its cwd, its
// executable, or an open file is HELD, whatever its age, and is never
// proposed. Where the OS cannot say (no procfs), the age bar rises to a day.
// A directory the sweeping user does not own is never proposed at all.

const (
	// scratchMinAge is how long a directory must have been untouched, by its
	// newest file, before it can be scratch: longer than any suite the gate
	// runs, so age alone already separates a finished run from a live one
	// even before the held check.
	scratchMinAge = 2 * time.Hour
	// scratchMinAgeUnverified is the bar where a live process could not be
	// ruled out by asking the OS.
	scratchMinAgeUnverified = 24 * time.Hour
)

var (
	goBuildScratchRe = regexp.MustCompile(`^go-build\d+$`)
	// goTestTempRe is the shape t.TempDir() names a test's own directory:
	// the test's name, then digits.
	goTestTempRe = regexp.MustCompile(`^Test[A-Za-z0-9_]*\d+$`)
	// ciRunScratchRe is the shape os.MkdirTemp gives the scratch directory of a
	// local CI run (aphrollo ci run): a run that is killed leaves it behind.
	ciRunScratchRe = regexp.MustCompile(`^aphrollo-ci-run-\d+$`)
	// replayScratchRe is the shape os.MkdirTemp gives the work directory of the
	// release replay (tools/replay): its clones of the repository and the builds
	// made from them, 8.7 GB each on a Linux box when a run was killed before
	// its own cleanup. The tool removes it when it ends; this is the next
	// sweep, for the run that never got to.
	replayScratchRe = regexp.MustCompile(`^replay-\d+$`)
)

// scratchName reports whether a directory name is one of the shapes the
// gate's own runs create.
func scratchName(name string) bool {
	switch {
	case goBuildScratchRe.MatchString(name), goTestTempRe.MatchString(name), ciRunScratchRe.MatchString(name), replayScratchRe.MatchString(name):
		return true
	case strings.HasPrefix(name, "aphrollo-lane-"):
		return true
	case strings.HasPrefix(name, "aphrollo-"):
		return strings.Contains(name, "-stub") || strings.Contains(name, "-pkgtest-") || strings.Contains(name, "-fixture")
	}
	return false
}

// scratchHeldFn asks whether a live process holds dir, and whether the OS
// could answer at all. A seam: the sweep's rules are proved without a process.
var scratchHeldFn = dirHeldByProcess

// mutationRunHeldFn asks whether a live mutation run holds the box-wide run
// lock; while one does, no mutation area is swept, whichever tool it runs.
// (A live cargo-mutants was the only veto before, and a Go lane's gremlins
// run has no such process name.) A seam for the tests.
var mutationRunHeldFn = mutantsRunHeld

// gcTempScratch proposes the scratch directories directly inside dir that no
// live run holds.
func gcTempScratch(dir string, now time.Time) []GCCandidate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || !scratchName(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil || !ownedByCurrentUser(info) {
			continue
		}
		newest, size := dirNewestAndSize(path)
		if newest.IsZero() {
			newest = info.ModTime() // an empty directory is as old as itself
		}
		held, known := scratchHeldFn(path)
		bar := scratchMinAge
		if !known {
			bar = scratchMinAgeUnverified
		}
		idle := now.Sub(newest)
		if held || idle < bar {
			continue
		}
		out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindTempLitter,
			Reason: "scratch of a run that is over (no live process holds it), idle " + formatDays(idle)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
