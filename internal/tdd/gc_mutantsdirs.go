package tdd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Category (k): what the SHARDED mutation runner leaves beside a checkout.
// One area per checkout, `<parent>/.mutants/<checkout name>`, and two kinds of
// directory inside it that gc used to treat as one:
//
//	shard-<i> is one process's output and temp — its mutants.out, its logs,
//	   and the tree copies its children wrote. Once no run is live it is pure
//	   leftover, whatever its age, exactly like the copies in the OS temp dir.
//	target-<i> is that shard's PERSISTENT build dir. It is kept on purpose:
//	   it is the whole reason a later run copies megabytes and still builds
//	   incrementally. Deleting one is legitimate when it is stale, and costs
//	   the next run a cold build, so the row says so — and it is interlocked
//	   on its own path, because it IS a cargo build directory and the repo's
//	   resolved target dir is a different lock guarding nothing here.
//
// Every liveness question fails safe, the same way issue #565 settled it: a
// directory whose owner cannot be determined keeps its protection.

const (
	gcMutantsShardPrefix  = "shard-"
	gcMutantsTargetPrefix = "target-"
)

// gcMutantsRunDirs proposes one area's reclaimable directories. A live
// cargo-mutants vetoes the whole area rather than only the directory it is
// writing to: a shard that finished early still holds the outcomes file the
// merge reads when the last shard lands, and a sweep that took it would
// delete a verdict that was reached.
func gcMutantsRunDirs(area string, olderThan time.Duration, now time.Time) []GCCandidate {
	if mutantsRunningFn() {
		return nil
	}
	var out []GCCandidate
	for _, e := range readDir(area) {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(area, e.Name())
		newest, size := dirNewestAndSize(path)
		if newest.IsZero() {
			continue
		}
		idle := now.Sub(newest)
		switch {
		case strings.HasPrefix(e.Name(), gcMutantsTargetPrefix):
			if _, live := targetDirOwnerFn(path); live || idle < olderThan {
				continue
			}
			out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindMutantsTarget,
				Reason: "mutation-run build dir, kept warm on purpose — the next run there builds cold, idle " +
					formatDays(idle)})
		case strings.HasPrefix(e.Name(), gcMutantsShardPrefix):
			// No age bar, for the same reason the tree copies in the OS temp
			// dir have none: ownership decides. A shard directory whose run
			// is over is garbage the moment that process exits, and one an
			// hour old is exactly as dead as one a week old.
			if _, live := mutantsCopyOwnerFn(path); live {
				continue
			}
			out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindMutants,
				Reason: "mutation-run shard dir whose run is over (tree copies and logs), idle " + formatDays(idle)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// gcMutantsOrphanAreas proposes an entire measurement area — not merely the
// stale entries inside it, which is all gcMutantsRunDirs and gcMutantsTrees
// ever look at — once the checkout beside it is gone. prGateMergedCheckout's
// cleanup (premergepr.go) now removes its own area along with its throwaway
// checkout, but a run that never reached cleanup, or one measured before
// that fix landed, leaves exactly this shape behind: the checkout gone, the
// area still full. Nothing else here ever asks that question, which is how
// eight of these — 21-161 MB each — went unseen on one box.
//
// The same liveness rule as its siblings: a live cargo-mutants vetoes the
// whole category, because the checkout it is building the merge in can
// disappear and reappear as `git worktree add`/`remove` run around it, and
// "gone this instant" is not the same claim as "gone for good".
func gcMutantsOrphanAreas(areas []string, now time.Time) []GCCandidate {
	if mutantsRunningFn() {
		return nil
	}
	var out []GCCandidate
	for _, area := range areas {
		checkout := filepath.Join(filepath.Dir(filepath.Dir(area)), filepath.Base(area))
		if _, err := os.Stat(checkout); err == nil {
			continue // its checkout is still here; not this category's to decide
		}
		newest, size := dirNewestAndSize(area)
		if newest.IsZero() {
			continue
		}
		out = append(out, GCCandidate{Path: area, Size: size, Kind: GCKindMutants,
			Reason: "measurement area whose checkout is gone, idle " + formatDays(now.Sub(newest))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// mutantsCopyDirs is every directory a leaked cargo-mutants tree copy can sit
// in: the OS temp dirs a BARE run copies into, and each checkout's own
// `.mutants` area, where a sharded run makes its copies beside its shard dirs.
//
// The area half is a RECOVERY path, not routine housekeeping. A run that
// completes deletes its own copies; only a killed or crashed one leaks. Of
// five lane areas measured on one box, four held nothing but a diff, and the
// fifth held 169 GB from two dead runs — 155 GB from one killed mid-copy and
// 14 GB from one that died before writing its outcomes. So liveness decides
// and nothing else: the two leaks were an order of magnitude apart, so any
// size bar that caught one would have missed the other, and both were hours
// old on the day they mattered, so an age bar would have caught neither.
func mutantsCopyDirs(areas []string) []string {
	dirs := make([]string, 0, len(areas)+2)
	dirs = append(dirs, areas...)
	return append(dirs, MutantsTempDirs()...)
}

// mutantsRunAreas is every `.mutants/<checkout>` area this repo can see: its
// own, the main checkout's, and every registered worktree's — and then every
// checkout directory inside those areas' parents, which is what finds the
// leftovers of a lane that has since been pruned.
//
// A sweep that looked only at the invoking checkout's own area is how 350 GB
// of a lane's mutation run stayed invisible while the table reported 18 GB:
// the run happens in the LANE, and the merge is swept from the primary.
func mutantsRunAreas(repo string) []string {
	seen := map[string]bool{}
	var areas []string
	for _, root := range strayTargetRoots(repo) {
		parent := filepath.Dir(measureTempDir(root))
		for _, e := range readDir(parent) {
			if !e.IsDir() {
				continue
			}
			area := filepath.Join(parent, e.Name())
			if seen[pathKey(area)] {
				continue
			}
			seen[pathKey(area)] = true
			areas = append(areas, area)
		}
	}
	sortStrings(areas)
	return areas
}
