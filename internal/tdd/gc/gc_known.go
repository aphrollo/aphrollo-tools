package gc

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The session-start sweep runs in whatever directory the session opened, and
// a session opened in a home directory sweeps no repository at all: the
// 3.1 GB of stale `.mutants` areas and temp copies on the box issue #1005 was
// filed against sat beside repos the sweep never looked at. The repos worth
// sweeping are the ones the gate has worked in, and the event log already records
// every root it judged, in the event log of its repo.

const (
	// knownReposWindow is how far back a repo counts as one the gate works in.
	knownReposWindow = 14 * 24 * time.Hour
	// knownReposMax bounds one sweep's walk.
	knownReposMax = 12
)

// KnownGCRepos lists the repositories the event logs name as worked in during
// the window, newest first, at most knownReposMax, each once per repository (a
// lane and its primary checkout are one). Only roots that still exist and
// resolve to a gate scratch directory count.
func KnownGCRepos() []string {
	now := time.Now()
	return knownReposFrom(readAllGateEntries(now.Add(-knownReposWindow)), now, knownReposMax)
}

// knownReposFrom reads at most limit repositories from entries, oldest first as
// the log gives them, so the newest are met first.
func knownReposFrom(entries []gateEntry, now time.Time, limit int) []string {
	seenRepo := map[string]bool{}
	var out []string
	for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := entries[i]
		if now.Sub(e.At) > knownReposWindow {
			continue
		}
		root := e.Root
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
