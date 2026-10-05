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
// directory. Loose files in `jobs` are not job directories and are left.

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

func gcLegacyJobs(dir string, now time.Time, live []string) []GCCandidate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, e.Name())
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
		out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindTempLitter,
			Reason: "legacy job directory, idle " + formatDays(now.Sub(newest))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
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
