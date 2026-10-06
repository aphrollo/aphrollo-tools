package gc

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Category (o): the coverage maps commit-time mutation keeps in the
// repository's shared git directory (mutation.CoverCacheDir). The kept maps are
// already bounded in number and size when one is saved; this removes what
// nothing has read for mutation.CoverCacheMaxAge, which is a package that no
// longer changes or exists, and the half-written file a killed save left. A
// removed map costs the next commit to that package one coverage run.
func gcCoverMaps(root string, now time.Time) []GCCandidate {
	dir := CoverCacheDir(root)
	if dir == "" {
		return nil
	}
	return gcCoverMapFiles(dir, CoverCacheMaxAge, now)
}

// gcCoverMapFiles proposes the maps of dir not read for maxAge, and the temp
// files of a save that never finished once they are a day old.
func gcCoverMapFiles(dir string, maxAge time.Duration, now time.Time) []GCCandidate {
	var out []GCCandidate
	for _, e := range readDir(dir) {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		age := maxAge
		switch {
		case strings.HasSuffix(name, ".json"):
		case strings.HasPrefix(name, ".testmap-"):
			age = tempLitterAge
		default:
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < age {
			continue
		}
		out = append(out, GCCandidate{
			Path:   filepath.Join(dir, name),
			Size:   info.Size(),
			Kind:   GCKindTempLitter,
			Reason: "commit-time coverage map not read for " + formatDays(now.Sub(info.ModTime())) + "; the next commit to that package measures it again",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
