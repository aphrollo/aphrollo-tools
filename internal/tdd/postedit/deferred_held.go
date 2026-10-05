package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeferredJobStartedWithin is when the newest deferred job recorded for the
// checkout at root, or a directory in it, started, when that is inside the
// window ending at now. A hook that detaches a build or a run leaves such a
// record before any result exists, so a builder whose first run is still going
// shows here although no session state has been stamped yet. It reads the
// directory without creating it, and never decodes more than it needs: a
// record it cannot read says nothing.
func DeferredJobStartedWithin(root string, window time.Duration, now time.Time) (time.Time, bool) {
	dir := deferredDirPath()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var newest time.Time
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".result.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		j, ok := decodeJob(data)
		if !ok || !deferredProjectWithin(j.Project, root) || now.Sub(j.Started) >= window {
			continue
		}
		if j.Started.After(newest) {
			newest = j.Started
		}
	}
	return newest, !newest.IsZero()
}
