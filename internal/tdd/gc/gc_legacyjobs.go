package gc

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Category (q): the legacy `jobs` directory of the Claude config dir, one
// subdirectory per background job of an older release, never removed. A job
// directory goes when nothing in it has been written for a week and no deferred
// record started in the last day points into it: a record names the paths a
// running or just-finished job writes to, so a job that is still live keeps its
// directory. Only a directory that shows the layout of the retired job runner
// (tmp/base-target, or a cargo target/) is ours to remove; any other is left
// and named by the dry run. Loose files in `jobs` are left too.

// legacyJobsMinAge is how long a job directory must have sat untouched.
const legacyJobsMinAge = 7 * 24 * time.Hour

// legacyJobsDir is the directory beside the gate state, in the same config dir.
func legacyJobsDir() string {
	state := StateDir()
	if state == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(state), "jobs")
}

// ownedJobDir reports whether a job directory shows the layout the retired job
// runner made: a tmp/base-target directory, or a cargo target/ directory (the
// CACHEDIR.TAG cargo writes into one, or a debug or release profile). Nothing
// else under jobs/ is a directory this tool made, and a directory that shows
// none of it is somebody's and is left alone.
func ownedJobDir(path string) bool {
	if isDir(filepath.Join(path, "tmp", "base-target")) {
		return true
	}
	target := filepath.Join(path, "target")
	if !isDir(target) {
		return false
	}
	if _, err := os.Stat(filepath.Join(target, "CACHEDIR.TAG")); err == nil {
		return true
	}
	return isDir(filepath.Join(target, "debug")) || isDir(filepath.Join(target, "release"))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// legacyJobDirs splits the job directories in dir into those that go (owned,
// idle a week, no live record pointing into them) and those left alone because
// they do not show they are ours.
func legacyJobDirs(dir string, now time.Time, live []string) (remove []GCCandidate, foreign []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // absence-ok: no jobs directory means nothing to sweep
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !ownedJobDir(path) {
			foreign = append(foreign, path)
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		newest, size := dirNewestAndSize(path)
		if newest.IsZero() {
			newest = info.ModTime()
		}
		if now.Sub(newest) < legacyJobsMinAge || pointedInto(path, live) {
			continue
		}
		remove = append(remove, GCCandidate{Path: path, Size: size, Kind: GCKindTempLitter,
			Reason: "legacy job directory, idle " + formatDays(now.Sub(newest))})
	}
	sort.Slice(remove, func(i, j int) bool { return remove[i].Path < remove[j].Path })
	sort.Strings(foreign)
	return remove, foreign
}

func gcLegacyJobs(dir string, now time.Time, live []string) []GCCandidate {
	remove, _ := legacyJobDirs(dir, now, live)
	return remove
}

// LegacyJobsLeftAlone lists the directories in the config dir's jobs directory
// that the sweep will not touch because they do not show they are ours, for
// the dry run to report.
func LegacyJobsLeftAlone() []string {
	dir := legacyJobsDir()
	if dir == "" {
		return nil
	}
	_, foreign := legacyJobDirs(dir, time.Now(), nil)
	return foreign
}

// pointedInto reports whether any of targets is dir or inside it.
func pointedInto(dir string, targets []string) bool {
	d := pathKey(dir)
	for _, t := range targets {
		k := pathKey(t)
		if k == d || strings.HasPrefix(k, d+string(filepath.Separator)) || strings.HasPrefix(k, d+"/") {
			return true
		}
	}
	return false
}
