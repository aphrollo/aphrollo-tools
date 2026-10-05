package mutation

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// What a full drive is full of. The doctor's disk line said "C: 1 GB free" and
// stopped there; the Windows box that fell to 1.1 GB held 53 GB of Go build
// cache, 42.8 GB of temp and a lane worktree for every merged PR, and the Linux
// box 218 GB of cache. When the drive is tight, the line now names those three
// and their sizes, biggest first, so the reader knows what to sweep. They are
// measured only then: walking a cache of hundreds of thousands of files is
// seconds of work a drive with room does not need.

// footprintWalkBudget bounds how long one directory's size may take to read. A
// directory the walk did not finish is reported as "at least" its size so far.
const footprintWalkBudget = 3 * time.Second

// goEnvTimeout bounds the `go env GOCACHE` the doctor runs.
const goEnvTimeout = 5 * time.Second

// doctorGoCacheDirFn answers `go env GOCACHE`; a seam so a test names a fake one.
var doctorGoCacheDirFn = goEnvCacheDir

func goEnvCacheDir() string {
	out, err := run.LightOutput(run.Spec{Name: "go", Args: []string{"env", "GOCACHE"}, Timeout: goEnvTimeout})
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		return ""
	}
	return dir
}

// laneWorktreesDir is where the repo's lane worktrees live: beside the repo, in
// .worktrees/<repo>, the layout `workspace create` uses. A path already inside
// that directory (a lane) resolves to it too.
func laneWorktreesDir(repo string) string {
	repo = filepath.Clean(repo)
	if parent := filepath.Dir(repo); filepath.Base(filepath.Dir(parent)) == ".worktrees" {
		return parent
	}
	return filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo))
}

// dirSizeWithin sums the file sizes under dir for at most budget. complete is
// false when the budget ended the walk first.
func dirSizeWithin(dir string, budget time.Duration) (size int64, complete bool) {
	deadline := time.Now().Add(budget)
	complete = true
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if time.Now().After(deadline) {
			complete = false
			return fs.SkipAll
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})
	return size, complete
}

type footprint struct {
	name     string
	size     int64
	complete bool
}

// footprintLine names the Go build cache, the lane worktrees and the temp dir
// with their sizes, biggest first.
func footprintLine(repo, cache string) string {
	var rows []footprint
	for _, d := range []struct{ name, dir string }{
		{"go build cache", cache},
		{"lane worktrees", laneWorktreesDir(repo)},
		{"temp", os.TempDir()},
	} {
		if d.dir == "" {
			continue
		}
		size, complete := dirSizeWithin(d.dir, footprintWalkBudget)
		rows = append(rows, footprint{d.name, size, complete})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].size > rows[j].size })
	parts := make([]string, len(rows))
	for i, r := range rows {
		more := ""
		if !r.complete {
			more = "+"
		}
		parts[i] = fmt.Sprintf("%s %s%s", r.name, formatBytes(r.size), more)
	}
	return strings.Join(parts, ", ")
}
