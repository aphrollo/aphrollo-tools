package tdd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Disk hygiene for the build caches this binary's own gates create and use.
// Five kinds of leftover qualify, and nothing else ever does:
//
//	(a) idle incremental caches in the invoking workspace's target dir --
//	    deleting one costs a single recompile of that crate;
//	(b) the gate's per-repo fail-first worktree / warm target whose repo is
//	    gone (origin.txt, written at creation, is the only thing that knows);
//	(c) a directory beside a registered external worktree that holds nothing
//	    but target/ -- git dropped the worktree, the build dir survived;
//	(h) a cargo target dir that is not THE target dir -- a hand-made
//	    `target-sky/` nobody builds into any more, idle for days;
//	(l) a stale ENTRY (never the directory itself) directly under the gate's
//	    own go-scratch directory, GoTmpRootDir -- every t.TempDir(), every
//	    os.MkdirTemp, and every compiled test binary a `go` runner stages
//	    lands there, one directory per suite run, and a run a timeout or a
//	    panic killed never gets to run its own cleanup. .mutants/, the
//	    mutation runner's own working area under the same root, is excluded
//	    by name, the same way deps/, build/ and .fingerprint/ are below.
//
// Everything else is somebody's work. In particular deps/, build/ and
// .fingerprint/ are NEVER reclaimable: they are what makes the next build
// incremental at all, so removing them turns a warm rebuild into a cold one.

// DefaultGCAge is how long an incremental cache must have gone untouched to
// be reclaimable. Three days: the cost of being wrong is one recompile of
// that one crate, and a crate nobody has touched in three days is not the
// crate whose rebuild anyone is waiting on.
const DefaultGCAge = 3 * 24 * time.Hour

// gcProtectedNames are the build-artifact directories no category may
// propose WHOLESALE: proposing one is proposing to cold-rebuild the world.
// The deps tiers are exempt because they name individual artifacts by
// cargo's own <crate>-<hash16> stem and an mtime bar — deps/ is reclaimable
// through the fingerprint shape, never by name.
var gcProtectedNames = map[string]bool{
	"deps":         true,
	"build":        true,
	".fingerprint": true,
}

// tempLitterAge is category (d)'s OWN age bar, deliberately shorter than the
// build-dir default: a lock file older than a day whose lock nobody holds
// cannot belong to a running build, and these accumulate by the hundred
// (871 measured in one operator's %TEMP%).
const tempLitterAge = 24 * time.Hour

// GCScope selects which categories a scan considers, so the git-shim hook
// (worktree-tied deletions only) and the manual command (everything) share
// one implementation.
type GCScope struct {
	Incremental     bool
	GateDirs        bool
	OrphanWorktrees bool
	TempLitter      bool
	Mutants         bool
	DepsArtifacts   bool
	StrayTargets    bool
	// LockAge overrides how old a lock file must be to count as litter.
	// Zero means tempLitterAge. An operator who knows the box is idle can
	// lower it; the unheld-lock probe is what makes that safe.
	LockAge time.Duration
}

// AllGCScopes is the manual command's scope: everything.
func AllGCScopes() GCScope {
	return GCScope{Incremental: true, GateDirs: true, OrphanWorktrees: true, TempLitter: true,
		Mutants: true, DepsArtifacts: true, StrayTargets: true}
}

// ScanGC collects the reclaimable directories for the workspace containing
// repo, each named once, sorted biggest-first so the table's first line is
// the one worth reading. It only ever READS.
func ScanGC(repo string, olderThan time.Duration, scope GCScope) []GCCandidate {
	// Absolute from here on: the command defaults to --repo ".", and a
	// candidate named relatively means a different directory the moment the
	// table is read (or acted on) from anywhere else.
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	var out []GCCandidate
	if scope.Incremental {
		out = append(out, gcIncremental(ResolveCargoTargetDir(repo), olderThan, time.Now())...)
	}
	if scope.GateDirs {
		if dir := StateDir(); dir != "" {
			out = append(out, gcStaleGateDirs(dir)...)
		}
		out = append(out, gcDeferredJobFiles(deferredDirPath(), deferredJobMaxAge, time.Now())...)
		out = append(out, gcGoTmpLitter(repo, olderThan, time.Now())...)
	}
	if scope.Mutants {
		// Every checkout's own area, not just this one's: a lane's mutation
		// run leaves its shard and build directories beside the LANE, and the
		// merge that would sweep them is run from the primary.
		areas := mutantsRunAreas(repo)
		for _, area := range areas {
			out = append(out, gcMutantsRunDirs(area, olderThan, time.Now())...)
			out = append(out, gcMutantsTrees(area, DefaultMutantsAge, time.Now())...)
		}
		// Whole areas, not only what is inside one: a checkout that is gone
		// for good (a throwaway GatePRMerge built and abandoned, or a lane
		// long since pruned) leaves an area neither category above ever
		// looks at as a unit.
		out = append(out, gcMutantsOrphanAreas(areas, time.Now())...)
		// The areas too, not only the OS temp dirs: a killed sharded run
		// leaks its tree copies INSIDE its own area, and a sweep handed only
		// the temp dirs reported 806.4 KB reclaimable with 169 GB of dead
		// copies beside a lane.
		out = append(out, gcMutantsTempCopies(mutantsCopyDirs(areas), time.Now())...)
		out = append(out, gcTempTargetDirs(MutantsTempDirs(), time.Now())...)
	}
	if scope.DepsArtifacts {
		out = append(out, gcDepsArtifacts(ResolveCargoTargetDir(repo), workspaceMemberCrates(repo),
			DefaultMemberArtifactAge, DefaultDepArtifactAge, time.Now())...)
	}
	if scope.TempLitter {
		for _, dir := range lockLitterDirs() {
			out = append(out, gcTempLitter(dir, scope.lockAge(), time.Now())...)
		}
	}
	if scope.OrphanWorktrees {
		if root := RepoRoot(repo); root != "" {
			out = append(out, gcOrphanWorktreeDirs(root)...)
		}
	}
	if scope.StrayTargets {
		out = append(out, gcStrayTargetDirs(strayTargetRoots(repo), ResolveCargoTargetDir(repo),
			olderThan, time.Now())...)
	}
	// One directory can qualify under two categories at once — an incremental
	// unit dir is both "an idle incremental cache" and "an idle unit dir of
	// crate X" — and a path proposed twice is printed twice and has its bytes
	// counted twice by a sweep. Deduped BEFORE the sort, so which category's
	// reason survives is decided by the order they run in rather than by how a
	// sort happened to break a tie between two rows of identical size.
	out = dedupeCandidates(out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// gcIncremental finds incremental caches under <target>/<profile>/incremental
// whose NEWEST file is older than olderThan. Newest, not the directory's own
// mtime: a nested rewrite does not touch the parent, so judging by the
// directory would delete a cache in use right now.
func gcIncremental(targetDir string, olderThan time.Duration, now time.Time) []GCCandidate {
	profiles, err := os.ReadDir(targetDir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, profile := range profiles {
		if !profile.IsDir() {
			continue
		}
		incDir := filepath.Join(targetDir, profile.Name(), "incremental")
		caches, err := os.ReadDir(incDir)
		if err != nil {
			continue
		}
		for _, cache := range caches {
			if !cache.IsDir() {
				continue
			}
			path := filepath.Join(incDir, cache.Name())
			if gcProtected(path) {
				continue
			}
			newest, size := dirNewestAndSize(path)
			idle := now.Sub(newest)
			if newest.IsZero() || idle < olderThan {
				continue
			}
			out = append(out, GCCandidate{
				Path:   path,
				Size:   size,
				Reason: fmt.Sprintf("incremental cache, idle %s", formatDays(idle)),
				Kind:   GCKindIncremental,
			})
		}
	}
	return out
}

// lockAge is the scope's litter bar, defaulting to a day.
func (s GCScope) lockAge() time.Duration {
	if s.LockAge > 0 {
		return s.LockAge
	}
	return tempLitterAge
}

// gcTempLitter finds aphrollo's own leavings in the lock dir: OWNER records
// whose lock nobody holds, and the compiled stub dirs a test binary builds.
// It never proposes a .lock file — the lock file IS the mutual exclusion, a
// holder keeps a handle to it, and on Windows a delete leaves the name in a
// delete-pending state whose failed open TryAcquireFileLock cannot tell from
// a free lock. Lock files are a few bytes each; what they exclude is a
// second cargo in one target dir.
func gcTempLitter(dir string, minAge time.Duration, now time.Time) []GCCandidate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "aphrollo-") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < minAge {
			continue
		}
		if e.IsDir() {
			if !strings.Contains(name, "-stub-") && !strings.Contains(name, "-pkgtest-") {
				continue
			}
			_, size := dirNewestAndSize(path)
			out = append(out, GCCandidate{Path: path, Size: size,
				Reason: "test stub dir, idle " + formatDays(now.Sub(info.ModTime())), Kind: GCKindTempLitter})
			continue
		}
		lock, isOwner := strings.CutSuffix(path, ".owner")
		if !isOwner || !strings.HasSuffix(lock, ".lock") {
			continue
		}
		// A held lock means a live build whose owner record a waiting session
		// is about to print: the acquire probe is the liveness test, and it
		// touches nothing but the record.
		release, free := TryAcquireFileLock(lock)
		if !free {
			continue
		}
		release()
		out = append(out, GCCandidate{Path: path, Size: info.Size(),
			Reason: "orphan owner record, idle " + formatDays(now.Sub(info.ModTime())), Kind: GCKindTempLitter})
	}
	return out
}

// gcStaleGateDirs finds the gate's own per-repo directories whose recorded
// origin no longer exists. A directory with no origin.txt (created before
// the record existed, or by something else entirely) is UNKNOWN and is left
// alone: guessing there deletes a warm target somebody is about to use.
func gcStaleGateDirs(base string) []GCCandidate {
	var out []GCCandidate
	// Only fail-first worktrees are gate-owned now: the gates build into the
	// repo's own target dir, so there is no gate cache to prune.
	for _, kind := range []string{"failfirst-wt"} {
		entries, err := os.ReadDir(filepath.Join(base, kind))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(base, kind, e.Name())
			origin, err := os.ReadFile(filepath.Join(path, gcOriginFile))
			if err != nil {
				continue
			}
			root := strings.TrimSpace(string(origin))
			if root == "" {
				continue
			}
			// Only a PROVEN-missing repo qualifies: any other Stat error
			// (permission, a disconnected drive, a path too long) means "I
			// could not look", and deleting on that basis takes out a live
			// gate worktree.
			if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
				continue
			}
			_, size := dirNewestAndSize(path)
			out = append(out, GCCandidate{
				Path:   path,
				Size:   size,
				Reason: fmt.Sprintf("gate %s for %s, which no longer exists", kind, root),
				Kind:   GCKindGateDir,
			})
		}
	}
	return out
}

// gcOrphanWorktreeDirs finds build-only leftovers beside repoRoot's
// registered external worktrees: same parent directory, not registered, and
// containing nothing but target/ (plus dotfiles). A registered worktree is
// never a candidate, and neither is a directory still holding source.
func gcOrphanWorktreeDirs(repoRoot string) []GCCandidate {
	registered := gitWorktreePaths(repoRoot)
	parents := map[string]bool{}
	for _, path := range registered {
		if insideDir(repoRoot, path) {
			continue // the main checkout and anything nested in it
		}
		parents[filepath.Dir(path)] = true
	}
	var out []GCCandidate
	for parent := range parents {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(parent, e.Name())
			if registered[pathKey(path)] != "" || !onlyBuildDirInside(path) {
				continue
			}
			_, size := dirNewestAndSize(path)
			out = append(out, GCCandidate{
				Path:   path,
				Size:   size,
				Reason: "orphan build dir — no worktree registered here anymore",
				Kind:   GCKindOrphanWorktree,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// gitWorktreePaths is the set of paths git itself considers registered
// worktrees of repoRoot, cleaned for comparison. Empty when git cannot
// answer -- and an empty set means NOTHING is proposed for deletion, which
// is the safe direction: with no registry there is no way to tell an orphan
// from a live worktree.
func gitWorktreePaths(repoRoot string) map[string]string {
	out, err := git(repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	paths := map[string]string{}
	for line := range strings.Lines(out) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok {
			paths[pathKey(rest)] = filepath.Clean(rest)
		}
	}
	return paths
}

// onlyBuildDirInside reports whether dir contains a target/ directory and
// nothing else that isn't a dotfile -- the shape of a worktree git removed
// and a build directory that survived it.
func onlyBuildDirInside(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	target := false
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if name == "target" && e.IsDir() {
			target = true
			continue
		}
		return false
	}
	return target
}

// ApplyGC removes each candidate, returning the bytes freed and the paths it
// REFUSED. A refusal is never silent: the whole point of the protected list
// is that a caller cannot talk the sweep into deleting an artifact dir.
func ApplyGC(cands []GCCandidate) (freed int64, refused []string) {
	for _, c := range cands {
		if gcProtected(c.Path) && !namesItsOwnArtifacts(c.Kind) {
			refused = append(refused, c.Path)
			continue
		}
		if err := os.RemoveAll(c.Path); err != nil {
			refused = append(refused, fmt.Sprintf("%s (%v)", c.Path, err))
			continue
		}
		freed += c.Size
	}
	return freed, refused
}

// ApplyGCFor applies a sweep for repo, respecting the one interlock the
// categories differ on: an incremental cache belongs to a target dir a build
// may be using right now, so those are deleted only while HOLDING a build
// slot for it, and are skipped (returned as skipped, not an error) when
// every slot is busy — the next sweep gets them. Everything else belongs to
// a repo or worktree that is already gone, so it never waits.
func ApplyGCFor(repo string, cands []GCCandidate) (freed int64, refused []string, skipped int) {
	// Anything that IS a cargo target dir — an incremental cache inside one,
	// an orphan lane build dir, a gate target — is deleted only while this
	// process holds that target's build slot. Otherwise a RemoveAll walks a
	// directory another session is compiling into.
	byTarget := map[string][]GCCandidate{}
	var free []GCCandidate
	for _, c := range cands {
		if target := gcTargetInterlock(repo, c); target != "" {
			byTarget[target] = append(byTarget[target], c)
			continue
		}
		free = append(free, c)
	}
	freed, refused = ApplyGC(free)
	// Map iteration order is randomized per range, not just per process, so
	// a sweep spanning more than one target-dir interlock (the repo's own
	// target dir plus a separate orphan worktree's own <path>/target is the
	// normal case once more than one stale worktree accumulates) appended
	// each bucket's refusals in an order that moved run to run on identical
	// input — sorted target keys make it deterministic.
	targets := make([]string, 0, len(byTarget))
	for target := range byTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		group := byTarget[target]
		// ONLY the target dir's own lock, never a global build slot: the
		// slots are the box's OOM/CPU governor, and a RemoveAll consumes
		// neither. Going through TryAcquireBuildSlot meant that when
		// unrelated builds held every slot, no candidate's target lock was
		// even attempted and every row came back skipped — the sweep
		// reclaimed nothing exactly when the box was full and disk was the
		// binding constraint. It also blurred what ok=false means, which is
		// what staleTargetLock below has to be able to trust: with the
		// target lock as the only question, a refusal means one thing —
		// another process holds THIS target dir.
		release, ok := TryAcquireFileLock(targetLockPath(target))
		if !ok {
			if !staleTargetLock(target) {
				skipped += len(group)
				continue
			}
			// The lock is guarding nobody: the build it was taken for is not on
			// the box any more, and no later sweep will get the slot either, so
			// waiting for it is a directory kept forever (issue #565: 7.5 GB idle
			// for three days, refused run after run).
			gFreed, gRefused := ApplyGC(group)
			freed += gFreed
			refused = append(refused, gRefused...)
			continue
		}
		gFreed, gRefused := ApplyGC(group)
		release()
		freed += gFreed
		refused = append(refused, gRefused...)
	}
	return freed, refused, skipped
}

// formatBytes renders a size the way an operator reads one.
