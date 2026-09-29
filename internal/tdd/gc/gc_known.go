package gc

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The session-start sweep runs in whatever directory the session opened, and
// a session opened in a home directory sweeps no repository at all: the
// 3.1 GB of stale `.mutants` areas and temp copies on the box issue #1005 was
// filed against sat beside repos the sweep never looked at. The repos worth
// sweeping are the ones the gate has worked in, and gate.log already records
// every root it judged.

const (
	// knownReposWindow is how far back a repo counts as one the gate works in.
	knownReposWindow = 14 * 24 * time.Hour
	// knownReposMax bounds one sweep's walk.
	knownReposMax = 12
	// knownReposTail is how much of the log's end is read.
	knownReposTail = 2 << 20
	// knownReposMaxLine is the longest log line read.
	knownReposMaxLine = 1 << 20
)

// KnownGCRepos lists the repositories gate.log names as worked in during the
// window, newest first, at most knownReposMax, each once per repository (a
// lane and its primary checkout are one). Only roots that still exist and
// resolve to a gate scratch directory count.
func KnownGCRepos() []string {
	dir := StateDir()
	if dir == "" {
		return nil
	}
	return knownReposFrom(filepath.Join(dir, "gate.log"), time.Now(), knownReposMax)
}

// knownReposFrom reads at most limit repositories from logPath's tail.
func knownReposFrom(logPath string, now time.Time, limit int) []string {
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil {
		_, _ = f.Seek(max(st.Size()-knownReposTail, 0), 0)
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, knownReposMaxLine)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	seenRepo := map[string]bool{}
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) < 3 {
			continue
		}
		at, err := time.Parse(time.RFC3339, fields[0])
		if err != nil || now.Sub(at) > knownReposWindow {
			continue
		}
		root := fields[2]
		if !filepath.IsAbs(root) {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			continue
		}
		key := GoTmpRootDir(root) // keyed on the primary checkout, so a lane and its repo agree
		if key == "" || seenRepo[key] {
			continue
		}
		seenRepo[key] = true
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}
